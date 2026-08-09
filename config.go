//go:build !windows
// +build !windows

package main

import (
	"encoding/json"
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
}

// IsConfigured reports whether enough Managed Configuration was found to
// safely report anything. WorkspaceSlug/ReportSecret have no default —
// unlike an earlier build of this agent, which shipped one real workspace's
// production secret hardcoded as the fallback here (baked into every
// compiled binary and readable in plaintext via `strings`). Never hardcode
// a real secret as a compiled-in default again — it belongs exclusively in
// the managed preference file, pushed per-fleet by whatever MDM deploys
// this agent (Applivery itself, e.g. via a Custom Settings payload
// targeting es.mi-labs.soar.agent).
func (c Config) IsConfigured() bool {
	return c.WorkspaceSlug != "" && c.ReportSecret != ""
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
		log.Printf("No managed config found at %s — WorkspaceSlug/ReportSecret must be set there before this agent can report anything.", configPath)
		return cfg
	}
	defer file.Close()

	if err := json.NewDecoder(file).Decode(&cfg); err != nil {
		log.Printf("Failed to parse config: %v. Using defaults.", err)
	}

	return cfg
}