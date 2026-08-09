//go:build !windows
// +build !windows

package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

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

func runAgentLoop(config Config, stopChan <-chan struct{}) {
	log.Println("Agent loop started. Reporting data...")

	interval := time.Duration(config.IntervalSec) * time.Second
	if interval < 30*time.Second {
		interval = 3600 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	gatherAndReport(config)

	for {
		select {
		case <-ticker.C:
			gatherAndReport(config)
		case <-stopChan:
			log.Println("Agent loop received stop signal. Shutting down gracefully.")
			return
		}
	}
}

func gatherAndReport(config Config) {
	if !config.IsConfigured() {
		log.Println("No WorkspaceSlug/ReportSecret in Managed Configuration yet — skipping this cycle. Deploy /Library/Preferences/es.mi-labs.soar.agent.json to start reporting.")
		return
	}

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
// payload so both DeviceData and AppsPayload can reuse it.
func sendWebhook(targetURL string, config Config, payload interface{}) {
	client := &http.Client{Timeout: 15 * time.Second}
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
		req.Header.Set("X-Device-Report-Secret", config.ReportSecret)

		resp, err := client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				log.Printf("Report to %s sent successfully.", targetURL)
				return
			}
			log.Printf("%s returned non-success status: %d", targetURL, resp.StatusCode)
		} else {
			log.Printf("Attempt %d: Error POSTing to %s: %v", i, targetURL, err)
		}

		time.Sleep(time.Duration(i) * 2 * time.Second)
	}
}