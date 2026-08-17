//go:build !windows
// +build !windows

package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Event Watches (macOS parity roadmap Phase 5) — the macOS mirror of the
// Windows agent's eventwatch_windows.go/etw_windows.go: admin-defined
// watches (Settings > Event-Driven Detection) that this agent monitors
// continuously between report cycles, instead of waiting for the next full
// poll to notice a change. Two watch types exist here, both intentionally
// disjoint from Windows' registryKey/etwProvider (see backend's
// eventWatches.schemas.ts WATCH_TYPES doc comment):
//
//   - "fsEventsPath": watches a file or directory for changes via fsnotify
//     (github.com/fsnotify/fsnotify) — kqueue-based on Darwin, pure Go, no
//     CGo. Chosen deliberately over raw FSEvents/CoreServices to keep the
//     "no local macOS build machine needed, CI (macos-latest) verifies it"
//     property the rest of this Go daemon has always had — the same
//     reasoning that steered the Windows agent away from more exotic ETW
//     bindings where a pure-Go option existed.
//   - "launchdJobState": watches a launchd job's (loaded, pid,
//     lastExitStatus) tuple for changes. There is no native "notify me when
//     a launchd job's state changes" API reachable from pure Go without
//     CGo (unlike Windows' RegNotifyChangeKeyValue or an ETW session), so
//     this polls `launchctl list <label>` on a short interval (3s) instead
//     — a deliberate, disclosed trade-off, not an oversight. Covers
//     load/unload transitions AND crash-restart loops (a changing PID or
//     LastExitStatus between polls), just via polling rather than a true
//     kernel notification.
//
// Both share the same debounce module (below) and the same
// diff-and-reconcile sync pattern the Windows agent uses: syncEventWatches
// is called once per report cycle (telemetry_macos.go's gatherAndReport),
// diffs the polled watch list against whichever watchers are currently
// running, and starts/stops/restarts to match — the watcher goroutines
// themselves then run independently until the next sync, same as Windows.

// ---- debouncer (ported from the Windows agent's eventwatch_windows.go;
// that repo has no separate debounce package either, it's inline there
// too) ----

type debouncer struct {
	mu     sync.Mutex
	timers map[string]*time.Timer
	counts map[string]int
}

func newDebouncer() *debouncer {
	return &debouncer{timers: make(map[string]*time.Timer), counts: make(map[string]int)}
}

// bump resets any pending timer for key, increments a per-key raw-event
// counter, and arms a new timer — fire only runs once `delay` passes
// without another bump, and receives the cumulative raw-event count seen
// since the last fire (or since the watch started).
func (d *debouncer) bump(key string, delay time.Duration, fire func(rawEventCount int)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.counts[key]++
	if t, ok := d.timers[key]; ok {
		t.Stop()
	}
	count := d.counts[key]
	d.timers[key] = time.AfterFunc(delay, func() {
		d.mu.Lock()
		d.counts[key] = 0
		d.mu.Unlock()
		fire(count)
	})
}

// stop cancels a key's pending timer — used when a watch is deleted/changed
// so a stale burst can't fire a notify after the watch it belonged to no
// longer exists.
func (d *debouncer) stop(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if t, ok := d.timers[key]; ok {
		t.Stop()
		delete(d.timers, key)
	}
	delete(d.counts, key)
}

var watcherDebouncer = newDebouncer()

// ---- config poll ----

type EventWatchDef struct {
	Key        string                 `json:"key"`
	WatchType  string                 `json:"watchType"`
	Params     map[string]interface{} `json:"params"`
	DebounceMs int                    `json:"debounceMs"`
}

// fetchEventWatches polls GET /api/device-data/event-watches?platform=macos
// once per report cycle — same header/client pattern as fetchCustomChecks
// (customchecks_macos.go). remoteIntervalSec (0 = no override) rides along
// in the same response, mirroring the Windows agent's Phase-4 hot
// ticker-reset feature — see maybeResetTicker in telemetry_macos.go.
func fetchEventWatches(baseURL *url.URL, config Config) ([]EventWatchDef, int) {
	watchesURL := baseURL.ResolveReference(&url.URL{Path: "/api/device-data/event-watches", RawQuery: "platform=macos"}).String()

	client := mtlsHTTPClient(15 * time.Second)
	req, err := http.NewRequest("GET", watchesURL, nil)
	if err != nil {
		log.Printf("Error building event-watches poll request: %v", err)
		return nil, 0
	}
	req.Header.Set("X-Workspace-Slug", config.WorkspaceSlug)
	applyLegacyAuthIfNeeded(req, config)

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error polling event watches: %v", err)
		return nil, 0
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Event watches poll returned HTTP %d — skipping this cycle: %s", resp.StatusCode, responseBodySnippet(resp))
		return nil, 0
	}

	var body struct {
		Items             []EventWatchDef `json:"items"`
		RemoteIntervalSec *int            `json:"remoteIntervalSec"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		log.Printf("Error decoding event watches response: %v", err)
		return nil, 0
	}
	remote := 0
	if body.RemoteIntervalSec != nil {
		remote = *body.RemoteIntervalSec
	}
	return body.Items, remote
}

// syncEventWatches is called once per report cycle from gatherAndReport
// (telemetry_macos.go), same as the Windows agent's own syncEventWatches
// call from its gatherAndReport. Returns the polled remoteIntervalSec for
// the ticker-reset logic.
func syncEventWatches(baseURL *url.URL, config Config) int {
	items, remoteIntervalSec := fetchEventWatches(baseURL, config)
	syncFsEventsWatches(items)
	syncLaunchdWatches(items)
	return remoteIntervalSec
}

// ---- watch manager state (shared by both watch types) ----

var watchManagerMu sync.Mutex

// ---- fsEventsPath ----

type fsWatch struct {
	key      string
	spec     string
	watcher  *fsnotify.Watcher
	stopChan chan struct{}
}

var activeFsWatches = make(map[string]*fsWatch)

// syncFsEventsWatches diffs the polled fsEventsPath watches against
// activeFsWatches — unchanged watches (same path/recursive/debounceMs) are
// left running, changed ones are stopped and restarted, new ones are
// started, and anything no longer polled is stopped. Mirrors the Windows
// agent's syncRegistryWatches diff shape exactly.
func syncFsEventsWatches(items []EventWatchDef) {
	watchManagerMu.Lock()
	defer watchManagerMu.Unlock()

	seen := make(map[string]bool)
	for _, item := range items {
		if item.WatchType != "fsEventsPath" {
			continue
		}
		path, _ := item.Params["path"].(string)
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		recursive, _ := item.Params["recursive"].(bool)
		debounceMs := item.DebounceMs
		if debounceMs <= 0 {
			debounceMs = 5000
		}
		seen[item.Key] = true
		spec := fmt.Sprintf("%s|%v|%d", path, recursive, debounceMs)

		if existing, ok := activeFsWatches[item.Key]; ok {
			if existing.spec == spec {
				continue
			}
			close(existing.stopChan)
			watcherDebouncer.stop(item.Key)
			delete(activeFsWatches, item.Key)
		}

		w, err := startFsWatch(item.Key, path, recursive, debounceMs)
		if err != nil {
			log.Printf("Could not start fsEventsPath watch %q for %s: %v", item.Key, path, err)
			continue
		}
		w.spec = spec
		activeFsWatches[item.Key] = w
	}

	for key, w := range activeFsWatches {
		if !seen[key] {
			close(w.stopChan)
			watcherDebouncer.stop(key)
			delete(activeFsWatches, key)
		}
	}
}

// startFsWatch opens an fsnotify watcher on path (walking and adding every
// subdirectory up front when recursive is true — see this file's top-of-
// file doc comment for the known limitation: a subdirectory created AFTER
// this walk isn't picked up until the next full restart or resync of this
// watch, since fsnotify has no native recursive-watch mode) and starts a
// goroutine that debounces every event it sees before firing a notify.
func startFsWatch(key, path string, recursive bool, debounceMs int) (*fsWatch, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	info, statErr := os.Stat(path)
	if statErr != nil {
		watcher.Close()
		return nil, statErr
	}

	if info.IsDir() && recursive {
		walkErr := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				_ = watcher.Add(p)
			}
			return nil
		})
		if walkErr != nil {
			log.Printf("fsEventsPath watch %q: error walking %s for recursive add: %v", key, path, walkErr)
		}
	} else if err := watcher.Add(path); err != nil {
		watcher.Close()
		return nil, err
	}

	stopChan := make(chan struct{})
	w := &fsWatch{key: key, watcher: watcher, stopChan: stopChan}

	go func() {
		defer watcher.Close()
		for {
			select {
			case _, ok := <-watcher.Events:
				if !ok {
					return
				}
				// Any event (write, create, remove, rename, chmod) counts —
				// this watch type doesn't discriminate by op the way a more
				// elaborate design could; matches the roadmap's own
				// "alert if this plist changes" framing, which doesn't
				// distinguish how it changed.
				watcherDebouncer.bump(key, time.Duration(debounceMs)*time.Millisecond, func(count int) {
					notifyEventFired(key, count)
				})
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("fsEventsPath watch %q (%s) error: %v", key, path, err)
			case <-stopChan:
				return
			}
		}
	}()

	return w, nil
}

// ---- launchdJobState ----

type launchdWatch struct {
	key      string
	label    string
	stopChan chan struct{}
}

var activeLaunchdWatches = make(map[string]*launchdWatch)

func syncLaunchdWatches(items []EventWatchDef) {
	watchManagerMu.Lock()
	defer watchManagerMu.Unlock()

	seen := make(map[string]bool)
	for _, item := range items {
		if item.WatchType != "launchdJobState" {
			continue
		}
		label, _ := item.Params["label"].(string)
		label = strings.TrimSpace(label)
		if label == "" {
			continue
		}
		debounceMs := item.DebounceMs
		if debounceMs <= 0 {
			debounceMs = 5000
		}
		seen[item.Key] = true

		if existing, ok := activeLaunchdWatches[item.Key]; ok {
			if existing.label == label {
				// debounceMs changes take effect on the NEXT restart of this
				// watch (e.g. triggered by a label change) rather than being
				// hot-applied mid-run — a minor simplification versus
				// Windows' registry watcher, acceptable since debounceMs
				// edits are rare and every watch still restarts on its next
				// resync whenever anything else about it changes too.
				continue
			}
			close(existing.stopChan)
			watcherDebouncer.stop(item.Key)
			delete(activeLaunchdWatches, item.Key)
		}

		activeLaunchdWatches[item.Key] = startLaunchdWatch(item.Key, label, debounceMs)
	}

	for key, w := range activeLaunchdWatches {
		if !seen[key] {
			close(w.stopChan)
			watcherDebouncer.stop(key)
			delete(activeLaunchdWatches, key)
		}
	}
}

// launchdPollInterval is how often a launchdJobState watch re-checks
// `launchctl list <label>` — short enough to feel event-driven for the
// "fast lane" this feature exists for, long enough not to matter as a
// resource cost even with several such watches active at once.
const launchdPollInterval = 3 * time.Second

// startLaunchdWatch polls getLaunchdJobState on launchdPollInterval and
// fires a debounced notify on any change to the (loaded, pid,
// lastExitStatus) tuple. The FIRST poll only establishes a baseline and
// never fires — mirrors the menu bar app's own StatusStore.lastViolationCount
// == -1 pattern (StatusStore.swift): a watch that starts on an
// already-unhealthy job shouldn't immediately fire just because its
// "previous" state was unknown.
func startLaunchdWatch(key, label string, debounceMs int) *launchdWatch {
	stopChan := make(chan struct{})
	w := &launchdWatch{key: key, label: label, stopChan: stopChan}

	go func() {
		ticker := time.NewTicker(launchdPollInterval)
		defer ticker.Stop()

		var lastState string
		haveBaseline := false

		for {
			select {
			case <-ticker.C:
				loaded, pid, lastExit := getLaunchdJobState(label)
				state := fmt.Sprintf("%v|%s|%s", loaded, pid, lastExit)
				if !haveBaseline {
					lastState = state
					haveBaseline = true
					continue
				}
				if state != lastState {
					lastState = state
					watcherDebouncer.bump(key, time.Duration(debounceMs)*time.Millisecond, func(count int) {
						notifyEventFired(key, count)
					})
				}
			case <-stopChan:
				return
			}
		}
	}()

	return w
}

// plistIntValueRe matches the "Key" = value; lines in launchctl's old-style
// (non-XML) property-list text output — e.g. `"PID" = 1234;` or
// `"LastExitStatus" = 0;`.
var plistIntValueRe = regexp.MustCompile(`"(\w+)"\s*=\s*(-?\d+);`)

func extractPlistValue(text, key string) string {
	for _, m := range plistIntValueRe.FindAllStringSubmatch(text, -1) {
		if m[1] == key {
			return m[2]
		}
	}
	return ""
}

// getLaunchdJobState runs `launchctl list <label>` (the single-argument
// form, which prints a full property-list block to stdout when the job is
// loaded, versus a nonzero exit / "Could not find service..." on stderr
// when it isn't — a different invocation from customchecks_macos.go's
// isLaunchdJobRunning, which uses the no-argument tabular form instead).
// pid/lastExit are "" when the job isn't loaded or those keys aren't
// present in its output.
func getLaunchdJobState(label string) (loaded bool, pid string, lastExit string) {
	out, err := exec.Command("launchctl", "list", label).Output()
	if err != nil {
		return false, "", ""
	}
	text := string(out)
	return true, extractPlistValue(text, "PID"), extractPlistValue(text, "LastExitStatus")
}

// ---- notify ----

type eventNotifyPayload struct {
	Platform        string `json:"platform"`
	SerialNumber    string `json:"serialNumber"`
	WatchKey        string `json:"watchKey"`
	ClientTimestamp string `json:"clientTimestamp,omitempty"`
	RawEventCount   int    `json:"rawEventCount,omitempty"`
}

// notifyEventFired is the macOS mirror of the Windows agent's identically-
// named function — reloads Config and the serial number FRESH at fire time
// (not captured when the watch started), matching this whole agent's "never
// trust a stale captured value" convention. POSTs via sendWebhook, so it
// gets the same retry/backoff/auth handling as every other outbound call —
// no separate local "already sent" cache is kept here; the backend's own
// 5s per-device cooldown (deviceData.service.ts) is the backstop against a
// double-send from e.g. an agent restart resetting this debouncer's state.
func notifyEventFired(watchKey string, rawEventCount int) {
	config := LoadConfig()
	if !config.IsConfigured() {
		log.Printf("Event watch %q fired but this agent isn't configured yet — ignoring.", watchKey)
		return
	}
	baseURL, err := url.Parse(config.BaseURL)
	if err != nil {
		log.Printf("Event watch %q fired but BaseURL is invalid: %v", watchKey, err)
		return
	}
	serialNumber := GetSerialNumber()
	if !isUsableSerial(serialNumber) {
		return
	}

	payload := eventNotifyPayload{
		Platform:        "macos",
		SerialNumber:    serialNumber,
		WatchKey:        watchKey,
		ClientTimestamp: time.Now().UTC().Format(time.RFC3339),
		RawEventCount:   rawEventCount,
	}
	notifyURL := baseURL.ResolveReference(&url.URL{Path: "/api/device-data/event-notify"}).String()
	sendWebhook(notifyURL, config, payload)
}
