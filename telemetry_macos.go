//go:build !windows
// +build !windows

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// responseBodySnippet reads and trims a response body for logging alongside
// a non-2xx status code — without this, a rejection from e.g. mTLS identity
// verification is invisible on this end (the backend's errorHandler
// middleware deliberately never logs an HttpError server-side either), so
// the only place the actual reason ever surfaces is this log line. Capped
// at 500 bytes — plenty for a JSON error detail, small enough to never
// bloat the launchd log even if a misconfigured proxy returns an HTML error
// page instead of JSON. Ported from the Windows agent's identical helper.
func responseBodySnippet(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
	return strings.TrimSpace(string(body))
}

type DeviceData struct {
	Platform     string                 `json:"platform"`
	SerialNumber string                 `json:"serialNumber"`
	Attributes   map[string]interface{} `json:"attributes"`
	// Custom Device Checks results (customchecks_macos.go) — omitted
	// entirely (not an empty object) when there are no checks configured
	// for this workspace/platform, so the backend's reportDeviceData()
	// carries forward whatever it already had instead of wiping it.
	CustomCheckResults map[string]CustomCheckResult `json:"customCheckResults,omitempty"`
}

// isUsableSerial rejects the values GetSerialNumber() itself falls back to
// on failure ("UNKNOWN", empty). Reporting under either would silently
// collide with every other device that also failed to read a real serial —
// the backend keys self-reported data by serial number, so two "UNKNOWN"
// devices would overwrite each other's attributes/apps in place. Better to
// skip the report (visible in this agent's own logs) than to attribute one
// device's data to another.
func isUsableSerial(serial string) bool {
	s := strings.ToUpper(strings.TrimSpace(serial))
	switch s {
	case "", "UNKNOWN":
		return false
	default:
		return true
	}
}

// triggerPollInterval is how often runAgentLoop checks for a force-report/
// force-evaluate marker file left by the menu bar app (Phase 3 — see
// agentstatus_macos.go's consumeTrigger doc comment). A plain os.Stat every
// couple of seconds is cheap enough to not need its own goroutine/channel —
// it just rides the same select alongside the normal report ticker. Ported
// from the Windows agent's identical constant in telemetry_windows.go.
const triggerPollInterval = 2 * time.Second

// remoteIntervalSecAtomic holds the latest SOAR-pushed report interval
// override (0 = none), set by gatherAndReport after each syncEventWatches
// call (eventwatch_macos.go) and read by maybeResetTicker below.
// Package-level + atomic rather than a return value threaded through every
// gatherAndReport call site: gatherAndReport is called from three places
// (runAgentLoop's initial call, its own ticker case, and checkTriggers'
// force-report path), and all three need the same "did the effective
// interval change, if so reset the ticker" follow-up. Ported from the
// Windows agent's identically-named variable in telemetry_windows.go
// (Phase 4 of the main event-driven-detection feature) — this piece was
// never backported to macOS until Event Watches (macOS parity roadmap
// Phase 5) needed somewhere for its own polled remoteIntervalSec to go.
var remoteIntervalSecAtomic int32

// clampInterval mirrors the floor this file has always applied: below 30s
// is almost certainly a misconfiguration (or an admin-cleared value reading
// back as 0), so fall back to the original 1h default rather than hammering
// the backend.
func clampInterval(sec int) time.Duration {
	interval := time.Duration(sec) * time.Second
	if interval < 30*time.Second {
		interval = 3600 * time.Second
	}
	return interval
}

// maybeResetTicker recomputes the effective report interval — this
// device's local Managed Configuration IntervalSec, unless a SOAR-pushed
// remoteIntervalSecAtomic override is present, in which case that wins —
// and calls ticker.Reset only when it actually changed. Ported from the
// Windows agent's identically-named function in telemetry_windows.go.
func maybeResetTicker(ticker *time.Ticker, current time.Duration) time.Duration {
	quietCfg, _ := loadConfigQuiet()
	sec := quietCfg.IntervalSec
	if remote := atomic.LoadInt32(&remoteIntervalSecAtomic); remote > 0 {
		sec = int(remote)
	}
	next := clampInterval(sec)
	if next != current {
		log.Printf("Report interval changed: %s -> %s — resetting ticker.", current, next)
		ticker.Reset(next)
	}
	return next
}

// runAgentLoop no longer takes a Config: it used to be loaded once at
// process start and cached for the whole run, which meant a Managed
// Configuration file landing after the agent was already running (e.g.
// launchd started it before an MDM script deployed the file) was silently
// ignored until the process restarted. gatherAndReport now reloads the
// config file fresh on every tick — same cadence Custom Device Checks
// already used — so config deployed after launch takes effect on the very
// next cycle. The report ticker itself also hot-reloads via
// maybeResetTicker above — IntervalSec changes, local or SOAR-pushed, take
// effect on the next loop iteration instead of needing a restart.
func runAgentLoop(stopChan <-chan struct{}) {
	log.Println("Agent loop started. Reporting data...")

	interval := clampInterval(LoadConfig().IntervalSec)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	triggerTicker := time.NewTicker(triggerPollInterval)
	defer triggerTicker.Stop()

	gatherAndReport()
	interval = maybeResetTicker(ticker, interval)

	for {
		select {
		case <-ticker.C:
			gatherAndReport()
			interval = maybeResetTicker(ticker, interval)
		case <-triggerTicker.C:
			checkTriggers()
			interval = maybeResetTicker(ticker, interval)
		case <-stopChan:
			log.Println("Agent loop received stop signal. Shutting down gracefully.")
			return
		}
	}
}

// checkTriggers is the daemon side of the menu bar app's "Force report"/
// "Force evaluate compliance" buttons — see agentstatus_macos.go's
// consumeTrigger doc comment for the full design. Each trigger file is
// consumed (deleted) the instant it's seen, so a click can never double-fire
// even if this tick and the menu bar app's write race. Ported from the
// Windows agent's identical function in telemetry_windows.go.
func checkTriggers() {
	if consumeTrigger(triggerReportPath()) {
		log.Println("Force report triggered from the menu bar app — running an immediate report cycle.")
		gatherAndReport()
	}
	if consumeTrigger(triggerEvaluatePath()) {
		log.Println("Force evaluate triggered from the menu bar app — requesting an immediate compliance evaluation.")
		forceEvaluateCompliance()
	}
}

func gatherAndReport() {
	config := LoadConfig()
	if !config.IsConfigured() {
		log.Println("No WorkspaceSlug plus ReportSecret/BootstrapToken in Managed Configuration yet — skipping this cycle. Deploy /Library/Preferences/es.mi-labs.soar.agent.json to start reporting.")
		return
	}

	// mTLS agent authentication (Phase 1 — see mtls_macos.go and
	// backend/docs/macos-agent-parity-roadmap.md §1) — checks/advances this
	// device's registration or renewal state every report cycle rather than
	// on a separate ticker. Always best-effort: never blocks or fails this
	// report cycle, whatever auth this device currently has (legacy secret,
	// or a valid certificate) is what sendWebhook/fetchCustomChecks below
	// will use. Takes the config already loaded above rather than loading
	// (and logging) its own copy.
	ensureMtlsIdentity(config)

	log.Println("Gathering telemetry...")

	baseURL, err := url.Parse(config.BaseURL)
	if err != nil {
		log.Printf("Invalid BaseURL in configuration: %v", err)
		return
	}

	serialNumber := GetSerialNumber()
	if !isUsableSerial(serialNumber) {
		log.Printf("Serial number %q is empty or unreadable — skipping this report to avoid colliding with another device's data.", serialNumber)
		return
	}
	log.Printf("Reporting as serial number %q — must match Applivery's own inventory exactly (case-sensitive) for the backend to attach this data to the right device.", serialNumber)

	attributes := GatherSecurityAttributes(config)

	// Custom Device Checks (Settings > Custom Device Checks) — polled fresh
	// every cycle, same as the fixed attributes above, so a check an admin
	// just created or edited takes effect on this device's very next report
	// without needing any Managed Configuration push.
	customCheckResults := runCustomChecks(fetchCustomChecks(baseURL, config))

	// Event Watches (macOS parity roadmap Phase 5, eventwatch_macos.go) —
	// diffs the polled watch list against whichever fsEventsPath/
	// launchdJobState watchers are currently running and starts/stops/
	// restarts to match; those watchers then run independently of this
	// ticker until the next sync. Best-effort like everything else in this
	// function — a poll failure here just means this cycle's watcher state
	// doesn't change, not that the report itself fails. The returned
	// remoteIntervalSec (0 = no override) is stashed for maybeResetTicker to
	// pick up right after this function returns (runAgentLoop's select
	// loop).
	remoteIntervalSec := syncEventWatches(baseURL, config)
	atomic.StoreInt32(&remoteIntervalSecAtomic, int32(remoteIntervalSec))

	payload := DeviceData{
		Platform:           "macos",
		SerialNumber:       serialNumber,
		Attributes:         attributes,
		CustomCheckResults: customCheckResults,
	}
	reportURL := baseURL.ResolveReference(&url.URL{Path: "/api/device-data/report"}).String()
	reportOK := sendWebhook(reportURL, config, payload)

	if config.ReportApps {
		apps := GetInstalledApps()
		appsPayload := AppsPayload{
			Platform:     "macos",
			SerialNumber: serialNumber,
			Apps:         apps,
		}
		appsURL := baseURL.ResolveReference(&url.URL{Path: "/api/device-data/report-apps"}).String()
		sendWebhook(appsURL, config, appsPayload)
	}

	updateStatusCache(baseURL, config, serialNumber, attributes, reportOK)
}

// updateStatusCache is the menu bar app's only data source (StatusCache's
// doc comment in agentstatus_macos.go has the full rationale): a local
// summary of what this cycle just reported, plus a fresh pull of this
// device's Compliance Policy status/score from the backend. Best-effort
// throughout — a failed compliance fetch (no Automation Credential
// configured yet, a transient network error, whatever) still lets the "what
// we reported" half of the cache update, with Compliance.Available left
// false and a human-readable reason for the status card to display. Ported
// from the Windows agent's identically-named function in
// telemetry_windows.go; reads GatherSecurityAttributes' own map keys
// (security_macos.go) rather than re-deriving them, since attributes is
// already what this same cycle just reported to the backend.
func updateStatusCache(baseURL *url.URL, config Config, serialNumber string, attributes map[string]interface{}, reportOK bool) {
	cache := StatusCache{
		WorkspaceSlug:     config.WorkspaceSlug,
		BaseURL:           config.BaseURL,
		SerialNumber:      serialNumber,
		LastReportAt:      time.Now().UTC().Format(time.RFC3339),
		LastReportOK:      reportOK,
		ReportedFileVault: config.ReportBitLocker,
		ReportedFirewall:  config.ReportFirewall,
		ReportedApps:      config.ReportApps,
	}
	if osBuild, ok := attributes["OsBuildNumber"].(string); ok {
		cache.OsBuild = osBuild
	}
	if v, ok := attributes["FileVaultEnabled"].(bool); ok {
		cache.FileVaultStatus = &v
	}
	if v, ok := attributes["FirewallEnabled"].(bool); ok {
		cache.FirewallEnabled = &v
	}

	status, err := fetchAgentStatus(baseURL, config, serialNumber, "macos")
	if err != nil {
		log.Printf("Could not fetch compliance status for the menu bar app: %v", err)
		cache.Compliance = AgentStatusCompliance{Available: false, Reason: "Could not reach the SOAR backend for compliance status."}
	} else {
		cache.Compliance = status.Compliance
		cache.DeviceMatched = status.Device.Matched
		if status.Device.DisplayName != nil {
			cache.DeviceName = *status.Device.DisplayName
		}
	}

	writeStatusCache(cache)
}

// sendWebhook is shared by both the attributes report and the (optional)
// app-inventory report — same endpoint family (POST /api/device-data/*),
// same header pair, same retry/backoff policy. Accepts any JSON-marshalable
// payload so both DeviceData and AppsPayload can reuse it. Goes through
// mtlsHTTPClient/applyLegacyAuthIfNeeded (mtls_macos.go) rather than
// deciding auth for itself — once this device has a client certificate,
// X-Device-Report-Secret is simply omitted and the certificate presented
// during the TLS handshake authenticates the request instead. Returns
// whether the report ultimately succeeded, used by gatherAndReport to
// record LastReportOK in the menu bar app's status cache — every caller
// before Phase 3 ignored this return value entirely, so behavior for
// existing callers is unchanged.
func sendWebhook(targetURL string, config Config, payload interface{}) bool {
	client := mtlsHTTPClient(15 * time.Second)
	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Error marshaling JSON payload: %v", err)
		return false
	}

	maxRetries := 3
	for i := 1; i <= maxRetries; i++ {
		req, err := http.NewRequest("POST", targetURL, bytes.NewBuffer(jsonData))
		if err != nil {
			log.Printf("Fatal error creating HTTP request: %v", err)
			return false
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Workspace-Slug", config.WorkspaceSlug)
		applyLegacyAuthIfNeeded(req, config)

		resp, err := client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				log.Printf("Report to %s sent successfully.", targetURL)
				return true
			}
			log.Printf("%s returned non-success status %d: %s", targetURL, resp.StatusCode, responseBodySnippet(resp))
		} else {
			log.Printf("Attempt %d: Error POSTing to %s: %v", i, targetURL, err)
		}

		time.Sleep(time.Duration(i) * 2 * time.Second)
	}
	return false
}