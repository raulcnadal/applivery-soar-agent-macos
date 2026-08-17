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

// runAgentLoop no longer takes a Config: it used to be loaded once at
// process start and cached for the whole run, which meant a Managed
// Configuration file landing after the agent was already running (e.g.
// launchd started it before an MDM script deployed the file) was silently
// ignored until the process restarted. gatherAndReport now reloads the
// config file fresh on every tick — same cadence Custom Device Checks
// already used — so config deployed after launch takes effect on the very
// next cycle. The initial load here only fixes the ticker's own interval
// for this process's lifetime; IntervalSec changes still need a restart to
// apply, which is an acceptable trade-off since a stuck/wrong interval is
// far less disruptive than "never picks up new config at all".
func runAgentLoop(stopChan <-chan struct{}) {
	log.Println("Agent loop started. Reporting data...")

	initial := LoadConfig()
	interval := time.Duration(initial.IntervalSec) * time.Second
	if interval < 30*time.Second {
		interval = 3600 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	gatherAndReport()

	for {
		select {
		case <-ticker.C:
			gatherAndReport()
		case <-stopChan:
			log.Println("Agent loop received stop signal. Shutting down gracefully.")
			return
		}
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
	// will use.
	ensureMtlsIdentity()

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

	payload := DeviceData{
		Platform:           "macos",
		SerialNumber:       serialNumber,
		Attributes:         attributes,
		CustomCheckResults: customCheckResults,
	}
	reportURL := baseURL.ResolveReference(&url.URL{Path: "/api/device-data/report"}).String()
	sendWebhook(reportURL, config, payload)

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
}

// sendWebhook is shared by both the attributes report and the (optional)
// app-inventory report — same endpoint family (POST /api/device-data/*),
// same header pair, same retry/backoff policy. Accepts any JSON-marshalable
// payload so both DeviceData and AppsPayload can reuse it. Goes through
// mtlsHTTPClient/applyLegacyAuthIfNeeded (mtls_macos.go) rather than
// deciding auth for itself — once this device has a client certificate,
// X-Device-Report-Secret is simply omitted and the certificate presented
// during the TLS handshake authenticates the request instead.
func sendWebhook(targetURL string, config Config, payload interface{}) {
	client := mtlsHTTPClient(15 * time.Second)
	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Error marshaling JSON payload: %v", err)
		return
	}

	maxRetries := 3
	for i := 1; i <= maxRetries; i++ {
		req, err := http.NewRequest("POST", targetURL, bytes.NewBuffer(jsonData))
		if err != nil {
			log.Printf("Fatal error creating HTTP request: %v", err)
			return
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Workspace-Slug", config.WorkspaceSlug)
		applyLegacyAuthIfNeeded(req, config)

		resp, err := client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				log.Printf("Report to %s sent successfully.", targetURL)
				return
			}
			log.Printf("%s returned non-success status %d: %s", targetURL, resp.StatusCode, responseBodySnippet(resp))
		} else {
			log.Printf("Attempt %d: Error POSTing to %s: %v", i, targetURL, err)
		}

		time.Sleep(time.Duration(i) * 2 * time.Second)
	}
}