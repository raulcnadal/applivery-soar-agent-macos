//go:build !windows
// +build !windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// appHashCacheEntry/the package-level cache persist SHA256 results for
// installed-app bundle executables across report cycles (Settings > Binary
// Integrity, backend/docs/settings.md#binary-integrity) — hashing every
// bundle's executable on every report cycle would mean streaming
// potentially hundreds of MB off disk each time for no reason, since
// installed binaries rarely change between reports (and GetInstalledApps
// is also called a second time per cycle by the custom-checks evaluator —
// see customchecks_macos.go's own doc comment — so an uncached hash would
// effectively run twice per app, twice as often as necessary). Keyed by the
// resolved executable's full path. A cached entry is trusted as long as the
// file's size and modification time still match what was last recorded;
// either changing means the binary was replaced (an update, or something
// more concerning) and must be re-hashed. Persisted to
// /Library/Application Support/Applivery/SOAR/apphashes.json — same
// directory as status.json (ipcDir(), agentstatus_macos.go) — via the same
// atomic temp-file-then-rename writer the mTLS keystore uses
// (writeKeystoreFileAtomic, mtls_macos.go), not status.json's non-atomic
// pattern: unlike status.json (whose reader already tolerates a corrupt/
// partial file), this cache's whole point is to be trustworthy input to a
// security check, so a torn write here is worth the extra temp+rename cost
// to avoid.
type appHashCacheEntry struct {
	Size    int64  `json:"size"`
	ModTime int64  `json:"modTime"` // Unix seconds
	SHA256  string `json:"sha256"`
}

var (
	appHashCacheMu    sync.Mutex
	appHashCacheData  map[string]appHashCacheEntry
	appHashCacheReady bool
)

func appHashCachePath() string {
	return filepath.Join(ipcDir(), "apphashes.json")
}

// loadAppHashCacheLocked lazily reads apphashes.json the first time it's
// needed in this process's lifetime. Must be called with appHashCacheMu
// held.
func loadAppHashCacheLocked() {
	if appHashCacheReady {
		return
	}
	appHashCacheReady = true
	appHashCacheData = make(map[string]appHashCacheEntry)
	data, err := os.ReadFile(appHashCachePath())
	if err != nil {
		return // first run, or file not yet created — empty cache is fine
	}
	var loaded map[string]appHashCacheEntry
	if err := json.Unmarshal(data, &loaded); err != nil {
		log.Printf("Could not parse app hash cache, starting fresh: %v", err)
		return
	}
	appHashCacheData = loaded
}

// saveAppHashCache flushes the in-memory hash cache to disk — called once
// at the end of GetInstalledApps (apps_macos.go), after every hashable app
// in this cycle has been processed, not after each individual hash.
func saveAppHashCache() {
	appHashCacheMu.Lock()
	loadAppHashCacheLocked()
	data, err := json.Marshal(appHashCacheData)
	appHashCacheMu.Unlock()
	if err != nil {
		return
	}
	if err := writeKeystoreFileAtomic(appHashCachePath(), data); err != nil {
		log.Printf("Could not write app hash cache: %v", err)
	}
}

// hashExecutableCached returns the lowercase-hex SHA256 of the file at
// exePath, reusing a cached result when the file's size+modtime haven't
// changed since it was last hashed. Empty exePath or an unreadable/missing
// file returns "" — callers treat that as "no hash available", never an
// error; binary-integrity is a best-effort enrichment that must never block
// or fail an app-inventory report.
func hashExecutableCached(exePath string) string {
	if exePath == "" {
		return ""
	}
	info, err := os.Stat(exePath)
	if err != nil || info.IsDir() {
		return ""
	}

	appHashCacheMu.Lock()
	loadAppHashCacheLocked()
	entry, ok := appHashCacheData[exePath]
	appHashCacheMu.Unlock()
	if ok && entry.Size == info.Size() && entry.ModTime == info.ModTime().Unix() {
		return entry.SHA256
	}

	sum, err := computeFileSha256(exePath)
	if err != nil {
		log.Printf("Could not hash %s: %v", exePath, err)
		return ""
	}

	appHashCacheMu.Lock()
	appHashCacheData[exePath] = appHashCacheEntry{Size: info.Size(), ModTime: info.ModTime().Unix(), SHA256: sum}
	appHashCacheMu.Unlock()
	return sum
}

// computeFileSha256 streams the file through SHA256 rather than reading it
// fully into memory first — app bundle executables can be hundreds of MB,
// and this potentially runs across hundreds of apps per report cycle.
func computeFileSha256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
