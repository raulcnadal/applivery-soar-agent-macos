//go:build !windows
// +build !windows

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
)

type Config struct {
	BaseURL         string `json:"base_url"`
	WorkspaceSlug   string `json:"workspace_slug"`
	ReportSecret    string `json:"report_secret"`
	IntervalSec     int    `json:"interval_sec"`
	ReportBitLocker bool   `json:"report_bitlocker"`
	ReportFirewall  bool   `json:"report_firewall"`
	ReportApps      bool   `json:"report_apps"`
	// BootstrapToken — mTLS agent authentication (see mtls_macos.go and
	// backend/docs/macos-agent-parity-roadmap.md §1). The SAME value pushed
	// to every device in the fleet via this Managed Configuration (a
	// "Global Bootstrap Token", not per-device or one-time — the backend
	// checks it against a live Applivery UEM serial-number lookup instead,
	// same mechanism as the Windows agent). Consumed by
	// ensureMtlsIdentity/registerMtlsIdentity on this device's first
	// successful registration (POST /api/device-mtls/register); harmless to
	// leave in place after that — a device that already has an active
	// certificate is never silently re-registered.
	BootstrapToken string `json:"bootstrap_token"`
	// RegisterURL — optional override for POST /api/device-mtls/register
	// ONLY. Falls back to BaseURL when empty (the historical, still fully
	// supported single-URL behavior). Exists because TLS client-certificate
	// verification is a whole-domain reverse-proxy setting, not scoped to a
	// path — a workspace using mTLS needs a SEPARATE subdomain/vhost
	// carrying the client-cert directives, and BaseURL points there once
	// configured. But /register never presents a client cert (the
	// bootstrap token is the credential) and doesn't need that vhost's
	// health at all — so setting RegisterURL to the ordinary dashboard
	// domain decouples first-time enrollment from whether the mTLS vhost
	// happens to be up. /renew is the opposite: it's always gated by
	// verified mTLS identity, so it deliberately keeps using BaseURL
	// unconditionally, never RegisterURL. Same field, same semantics as the
	// Windows agent's Config.RegisterURL.
	RegisterURL string `json:"register_url"`
}

// IsConfigured reports whether enough Managed Configuration was found to
// safely report anything. WorkspaceSlug has no default — unlike an earlier
// build of this agent, which shipped one real workspace's production secret
// hardcoded as the fallback here (baked into every compiled binary and
// readable in plaintext via `strings`). Never hardcode a real secret as a
// compiled-in default again — it belongs exclusively in the managed
// preference file, pushed per-fleet by whatever MDM deploys this agent
// (Applivery itself, e.g. via a Custom Settings payload targeting
// es.mi-labs.soar.agent).
//
// Either ReportSecret OR BootstrapToken alone is enough to proceed — an
// mTLS-only deployment (BootstrapToken set, ReportSecret intentionally
// blank) is a fully supported configuration, not a partial one, matching
// the Windows agent's own IsConfigured() fix for the same reasoning: hard-
// requiring ReportSecret unconditionally would silently block
// ensureMtlsIdentity from ever running on a device configured for
// bootstrap-token-only enrollment.
func (c Config) IsConfigured() bool {
	return c.WorkspaceSlug != "" && (c.ReportSecret != "" || c.BootstrapToken != "")
}

func LoadConfig() Config {
	cfg := Config{
		BaseURL:         "https://soar.mi-labs.es",
		WorkspaceSlug:   "",
		ReportSecret:    "",
		IntervalSec:     3600,
		ReportBitLocker: true,
		ReportFirewall:  true,
		ReportApps:      false,
	}

	configPath := "/Library/Preferences/es.mi-labs.soar.agent.json"
	file, err := os.Open(configPath)
	if err != nil {
		log.Printf("No managed config found at %s — WorkspaceSlug plus either ReportSecret or BootstrapToken must be set there before this agent can report anything.", configPath)
		return cfg
	}
	defer file.Close()

	if err := json.NewDecoder(file).Decode(&cfg); err != nil {
		log.Printf("Failed to parse config: %v. Using defaults.", err)
	}

	log.Printf(
		"Config loaded: BaseURL=%s RegisterURL=%s WorkspaceSlug=%s ReportSecret=%s BootstrapToken=%s ReportBitLocker=%v ReportFirewall=%v ReportApps=%v IntervalSec=%d",
		cfg.BaseURL, maskEmpty(cfg.RegisterURL), maskEmpty(cfg.WorkspaceSlug), maskSecret(cfg.ReportSecret), maskSecret(cfg.BootstrapToken), cfg.ReportBitLocker, cfg.ReportFirewall, cfg.ReportApps, cfg.IntervalSec,
	)

	return cfg
}

// maskEmpty and maskSecret exist purely so LoadConfig's summary line is
// actually useful for troubleshooting "the config was deployed but nothing
// is being reported" — printed every cycle now that config is reloaded each
// tick (see gatherAndReport in telemetry_macos.go), never the raw secret.
func maskEmpty(s string) string {
	if s == "" {
		return "(not set)"
	}
	return s
}
func maskSecret(s string) string {
	if s == "" {
		return "(not set)"
	}
	return fmt.Sprintf("(set, %d chars)", len(s))
}