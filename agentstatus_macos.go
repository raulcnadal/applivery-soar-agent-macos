//go:build !windows
// +build !windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// This file is the macOS mirror of the Windows agent's internal/agentstatus
// package: the file-based IPC contract this daemon (running as root via
// LaunchDaemon) shares with the SwiftUI menu bar app (Phase 3 of
// backend/docs/macos-agent-parity-roadmap.md, running unprivileged via a
// per-console-user LaunchAgent). The JSON tags below MUST stay in lockstep
// with MenuBarApp/Sources/AppliverySOARMenuBar/StatusCache.swift — if you
// rename or retype a field here, make the matching change there too, or the
// menu bar app will silently fail to decode status.json (Swift's
// JSONDecoder is strict about types, though it does tolerate unknown/extra
// keys and missing Optional ones).
//
// This repo has no internal/ package split like the Windows agent does —
// every macOS-specific file lives flat in package main — so this is a
// single file rather than its own package, but the type/field shapes below
// are otherwise a deliberate line-for-line port.

// AgentStatusPolicy mirrors backend GET /api/device-data/agent-status's
// per-policy shape (id/name/severity only — pass/fail is derived from
// Violations by whoever renders the card, not stored redundantly here).
type AgentStatusPolicy struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Severity string `json:"severity"`
}

type AgentStatusViolation struct {
	PolicyID       string  `json:"policyId"`
	PolicyName     *string `json:"policyName"`
	Severity       *string `json:"severity"`
	LastDetectedAt *string `json:"lastDetectedAt"`
}

type AgentStatusCompliance struct {
	Available  bool                    `json:"available"`
	Reason     string                  `json:"reason,omitempty"`
	Compliant  bool                    `json:"compliant,omitempty"`
	RiskScore  *int                    `json:"riskScore,omitempty"`
	RiskTier   *string                 `json:"riskTier,omitempty"`
	Policies   []AgentStatusPolicy     `json:"policies"`
	Violations []AgentStatusViolation  `json:"violations,omitempty"`
}

type AgentStatusDevice struct {
	Matched     bool    `json:"matched"`
	ID          *string `json:"id"`
	DisplayName *string `json:"displayName"`
}

// AgentStatusResponse is what GET /api/device-data/agent-status returns —
// decoded directly in status_macos.go's fetchAgentStatus, then folded into
// StatusCache below by updateStatusCache/forceEvaluateCompliance.
type AgentStatusResponse struct {
	Device     AgentStatusDevice     `json:"device"`
	Compliance AgentStatusCompliance `json:"compliance"`
}

// StatusCache is the on-disk status.json contract: this daemon writes it
// after every report cycle (telemetry_macos.go's updateStatusCache) and
// whenever a forced evaluation completes (status_macos.go's
// forceEvaluateCompliance); the menu bar app reads it on open plus a 60s
// timer — identical cadence to the Windows tray (tray/main.go's
// refreshIntervalMs). Field names/JSON tags are intentionally identical to
// the Windows agent's own StatusCache so this repo's documentation of the
// contract reads the same on both platforms, with one deliberate exception:
// "BitLocker" is Windows-specific terminology, so the macOS equivalent
// fields are named ReportedFileVault/FileVaultStatus/reportedFileVault/
// fileVaultStatus instead — the JSON key changes, everything else (gate +
// nullable bool pattern) is identical.
type StatusCache struct {
	UpdatedAt         string                `json:"updatedAt"`
	WorkspaceSlug     string                `json:"workspaceSlug"`
	BaseURL           string                `json:"baseUrl"`
	SerialNumber      string                `json:"serialNumber"`
	LastReportAt      string                `json:"lastReportAt"`
	LastReportOK      bool                  `json:"lastReportOk"`
	ReportedFileVault bool                  `json:"reportedFileVault"`
	FileVaultStatus   *bool                 `json:"fileVaultStatus,omitempty"`
	ReportedFirewall  bool                  `json:"reportedFirewall"`
	FirewallEnabled   *bool                 `json:"firewallEnabled,omitempty"`
	ReportedApps      bool                  `json:"reportedApps"`
	OsBuild           string                `json:"osBuild,omitempty"`
	DeviceMatched     bool                  `json:"deviceMatched"`
	DeviceName        string                `json:"deviceName,omitempty"`
	Compliance        AgentStatusCompliance `json:"compliance"`
}

// ipcDir is the shared directory this daemon and the menu bar app both use
// for status.json plus the two trigger-*.flag files — the macOS mirror of
// the Windows agent's %ProgramData%\Applivery\SOAR\ directory. This daemon
// creates it (root-owned, MkdirAll 0755) on first write; writeStatusCache
// below additionally loosens the directory's own mode so the unprivileged
// menu bar app can create trigger files in it (see its doc comment).
func ipcDir() string {
	return "/Library/Application Support/Applivery/SOAR"
}

func statusCachePath() string     { return filepath.Join(ipcDir(), "status.json") }
func triggerReportPath() string   { return filepath.Join(ipcDir(), "trigger-report.flag") }
func triggerEvaluatePath() string { return filepath.Join(ipcDir(), "trigger-evaluate.flag") }

// consumeTrigger mirrors the Windows agent's agentstatus.ConsumeTrigger:
// existence is the only signal, content is never read (WriteTrigger on the
// Swift side writes an RFC3339 timestamp purely for human debugging, e.g.
// `cat trigger-report.flag` while troubleshooting). Returns false — not an
// error — when the flag simply isn't present, which is the normal case on
// nearly every poll.
func consumeTrigger(path string) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	_ = os.Remove(path)
	return true
}

// writeStatusCache is best-effort by design, matching the Windows agent's
// own doc comment on its identically-named function: a failure here (disk
// full, directory somehow removed underneath this process) must never fail
// or block the report cycle that produced this data — the menu bar app
// simply keeps showing whatever it last read until the next successful
// write. Not atomic (direct os.WriteFile, no temp+rename) for the same
// reason the Windows implementation isn't: status.json is read-mostly,
// single-writer (this daemon), and the reader already treats any decode
// error as "no data yet" rather than crashing.
//
// After writing, chmod's the IPC directory itself to 0o1777 (world-
// writable + sticky, the same mode /tmp uses) — every other file this
// daemon creates under it (status.json, at 0644) stays root-owned and
// read-only to everyone else, so the sticky bit's job here is narrow: it
// lets the unprivileged, per-console-user menu bar app CREATE
// trigger-report.flag/trigger-evaluate.flag in this directory (which a
// plain 0755 root-owned directory would forbid), while still preventing
// that same unprivileged process from deleting or renaming status.json out
// from under this daemon (which a plain world-writable 0777 directory,
// without the sticky bit, would allow regardless of status.json's own
// file-level permissions). Re-applied on every write rather than only at
// first creation so an admin who resets permissions by hand is corrected
// again on this device's very next report cycle.
func writeStatusCache(c StatusCache) {
	c.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(ipcDir(), 0755); err != nil {
		return
	}
	_ = os.Chmod(ipcDir(), 0o1777)
	_ = os.WriteFile(statusCachePath(), data, 0644)
}
