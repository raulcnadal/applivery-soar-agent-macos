//go:build !windows
// +build !windows

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type InstalledApp struct {
	Identifier string `json:"identifier"`
	Name       string `json:"name"`
	Version    string `json:"version,omitempty"`
	// SHA256 is the lowercase-hex SHA256 of the app bundle's main
	// executable (Contents/MacOS/<CFBundleExecutable>), when that key was
	// present in Info.plist and the file was readable. Feeds SOAR's Binary
	// Integrity feature (backend/docs/settings.md#binary-integrity):
	// VirusTotal file-reputation lookup to flag sideloaded/tampered
	// binaries, independent of CVE/version-based vulnerability matching.
	// Self-reported only — Applivery's own server-fetched app inventory has
	// no equivalent field. Omitted (empty) rather than defaulted whenever
	// CFBundleExecutable is missing or the executable couldn't be read —
	// see hashExecutableCached's doc comment (apphashcache_macos.go).
	SHA256 string `json:"sha256,omitempty"`
}

type AppsPayload struct {
	Platform     string         `json:"platform"`
	SerialNumber string         `json:"serialNumber"`
	Apps         []InstalledApp `json:"apps"`
}

// GetInstalledApps scans standard macOS application directories and parses Info.plist
// matching the behavior of report-installed-apps.sh
func GetInstalledApps() []InstalledApp {
	var apps []InstalledApp
	seen := make(map[string]bool)

	searchDirs := []string{
		"/Applications",
		"/System/Applications",
	}

	// Dynamically include user application folders
	userDirs, err := os.ReadDir("/Users")
	if err == nil {
		for _, uDir := range userDirs {
			if uDir.IsDir() && !strings.HasPrefix(uDir.Name(), ".") && uDir.Name() != "Shared" {
				searchDirs = append(searchDirs, filepath.Join("/Users", uDir.Name(), "Applications"))
			}
		}
	}

	for _, dir := range searchDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() && strings.HasSuffix(entry.Name(), ".app") {
				appPath := filepath.Join(dir, entry.Name())
				infoPlistPath := filepath.Join(appPath, "Contents", "Info.plist")

				if _, err := os.Stat(infoPlistPath); err != nil {
					continue
				}

				appInfo := parseInfoPlist(infoPlistPath, entry.Name(), appPath)
				if appInfo.Identifier != "" && !seen[appInfo.Identifier] {
					seen[appInfo.Identifier] = true
					apps = append(apps, appInfo)
				}
			}
		}
	}

	saveAppHashCache()
	return apps
}

// parseInfoPlist reads appPath/Contents/Info.plist (already located by the
// caller as plistPath) and, separately, attempts to hash the bundle's main
// executable at appPath/Contents/MacOS/<CFBundleExecutable> — a bundle
// missing CFBundleExecutable (rare, but happens for some plugin-style
// bundles that got swept up by the ".app" directory scan) or with an
// unreadable executable just yields an empty SHA256, same "best-effort
// enrichment, never a hard failure" contract as everything else here.
func parseInfoPlist(plistPath string, defaultFolderName string, appPath string) InstalledApp {
	// Convert binary or XML plist to JSON using native macOS plutil utility
	cmd := exec.Command("plutil", "-convert", "json", "-o", "-", plistPath)
	out, err := cmd.Output()
	if err != nil {
		fallbackName := strings.TrimSuffix(defaultFolderName, ".app")
		return InstalledApp{
			Name: fallbackName,
		}
	}

	var plistData map[string]interface{}
	if err := json.Unmarshal(out, &plistData); err != nil {
		fallbackName := strings.TrimSuffix(defaultFolderName, ".app")
		return InstalledApp{
			Name: fallbackName,
		}
	}

	identifier, _ := plistData["CFBundleIdentifier"].(string)
	if identifier == "" {
		identifier = strings.TrimSuffix(defaultFolderName, ".app")
	}

	name, _ := plistData["CFBundleDisplayName"].(string)
	if name == "" {
		name, _ = plistData["CFBundleName"].(string)
	}
	if name == "" {
		name = strings.TrimSuffix(defaultFolderName, ".app")
	}

	version, _ := plistData["CFBundleShortVersionString"].(string)
	if version == "" {
		version, _ = plistData["CFBundleVersion"].(string)
	}

	var sha256Hash string
	if executable, _ := plistData["CFBundleExecutable"].(string); strings.TrimSpace(executable) != "" {
		exePath := filepath.Join(appPath, "Contents", "MacOS", strings.TrimSpace(executable))
		sha256Hash = hashExecutableCached(exePath)
	}

	return InstalledApp{
		Identifier: strings.TrimSpace(identifier),
		Name:       strings.TrimSpace(name),
		Version:    strings.TrimSpace(version),
		SHA256:     sha256Hash,
	}
}