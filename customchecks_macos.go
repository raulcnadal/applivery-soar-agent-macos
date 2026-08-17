//go:build !windows
// +build !windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Custom Device Checks — admin-defined queries authored in Settings > Custom
// Device Checks (backend's customChecks.service.ts has the full design).
// This agent polls GET /api/device-data/custom-checks?platform=macos once
// per report cycle, runs every enabled check it gets back locally, and
// includes the results in the SAME POST /api/device-data/report call
// telemetry_macos.go already makes (customCheckResults field) — no separate
// report call, no local persistence between cycles.
//
// CustomCheckResult.Error is set ONLY when the check itself failed to run
// (launchd job not loaded, plist key missing, command timeout) — a
// legitimately negative result (process not running, service stopped) is a
// normal Value, not an Error. The backend's compliance evaluator treats an
// errored result the same as "missing" (complianceEvaluate.ts). Mirrors the
// Windows agent's customchecks_windows.go 1:1 in structure/semantics —
// only the underlying OS calls differ (pgrep/launchctl/plutil/bash vs
// WMI/svcmgr/registry/powershell).

type CustomCheckDef struct {
	Key         string                 `json:"key"`
	CheckerType string                 `json:"checkerType"`
	Params      map[string]interface{} `json:"params"`
}

type CustomCheckResult struct {
	Value interface{} `json:"value,omitempty"`
	Error string      `json:"error,omitempty"`
}

func fetchCustomChecks(baseURL *url.URL, config Config) []CustomCheckDef {
	checksURL := baseURL.ResolveReference(&url.URL{Path: "/api/device-data/custom-checks", RawQuery: "platform=macos"}).String()

	client := mtlsHTTPClient(15 * time.Second)
	req, err := http.NewRequest("GET", checksURL, nil)
	if err != nil {
		log.Printf("Error building custom-checks poll request: %v", err)
		return nil
	}
	req.Header.Set("X-Workspace-Slug", config.WorkspaceSlug)
	applyLegacyAuthIfNeeded(req, config)

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error polling custom checks: %v", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Custom checks poll returned HTTP %d — skipping this cycle's custom checks: %s", resp.StatusCode, responseBodySnippet(resp))
		return nil
	}

	var body struct {
		Items []CustomCheckDef `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		log.Printf("Error decoding custom checks response: %v", err)
		return nil
	}
	return body.Items
}

func runCustomChecks(checks []CustomCheckDef) map[string]CustomCheckResult {
	if len(checks) == 0 {
		return nil
	}
	results := make(map[string]CustomCheckResult, len(checks))
	for _, c := range checks {
		results[c.Key] = runOneCustomCheck(c)
	}
	return results
}

func runOneCustomCheck(c CustomCheckDef) CustomCheckResult {
	switch c.CheckerType {
	case "processRunning":
		name, _ := c.Params["processName"].(string)
		running, err := isProcessRunning(name)
		if err != nil {
			return CustomCheckResult{Error: err.Error()}
		}
		return CustomCheckResult{Value: running}

	case "serviceStatus":
		label, _ := c.Params["serviceName"].(string)
		running, err := isLaunchdJobRunning(label)
		if err != nil {
			return CustomCheckResult{Error: err.Error()}
		}
		return CustomCheckResult{Value: running}

	case "registryOrFileValue":
		path, _ := c.Params["path"].(string)
		plistKey, _ := c.Params["plistKey"].(string)
		val, err := readFileOrPlistValue(path, plistKey)
		if err != nil {
			return CustomCheckResult{Error: err.Error()}
		}
		return CustomCheckResult{Value: val}

	case "appInstalled":
		identifier, _ := c.Params["identifier"].(string)
		version, err := findInstalledAppVersion(identifier)
		if err != nil {
			return CustomCheckResult{Error: err.Error()}
		}
		return CustomCheckResult{Value: version}

	case "command":
		command, _ := c.Params["command"].(string)
		out, err := runCustomCommand(command)
		if err != nil {
			return CustomCheckResult{Error: err.Error()}
		}
		return CustomCheckResult{Value: out}

	default:
		return CustomCheckResult{Error: fmt.Sprintf("unknown checker type %q", c.CheckerType)}
	}
}

// isProcessRunning returns (false, nil) — not an error — when the process
// simply isn't running; an error return means pgrep itself failed to run.
// `-x` matches the exact process name (same convention as the Windows
// agent's WMI exact-name match); `-i` makes it case-insensitive.
func isProcessRunning(name string) (bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return false, fmt.Errorf("no process name configured")
	}
	cmd := exec.Command("pgrep", "-ix", name)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		// pgrep's own documented "no processes matched" exit code — a normal
		// negative result, not a failure.
		return false, nil
	}
	return false, fmt.Errorf("running pgrep: %v", err)
}

// isLaunchdJobRunning treats "job not loaded" as an error (can't determine
// state), distinct from "loaded but not running" (a normal false) — same
// reasoning as the Windows agent's isServiceRunning. `launchctl list`
// (no argument) prints one line per loaded job as "PID\tStatus\tLabel";
// PID is "-" when the job is loaded but not currently running.
func isLaunchdJobRunning(label string) (bool, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return false, fmt.Errorf("no launchd label configured")
	}
	out, err := exec.Command("launchctl", "list").Output()
	if err != nil {
		return false, fmt.Errorf("running launchctl list: %v", err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		pid := fields[0]
		jobLabel := fields[len(fields)-1]
		if jobLabel == label {
			return pid != "-", nil
		}
	}
	return false, fmt.Errorf("launchd job %q not found (not loaded)", label)
}

// readFileOrPlistValue has two modes, matching customChecks.schemas.ts's
// validateCheckParams: with plistKey set, extracts that key's raw value via
// `plutil -extract` (works directly on XML or binary plists, no `defaults`
// domain-resolution ambiguity when running as root outside a user session);
// with plistKey blank, it's a plain existence check — a normal true/false
// result, never an error, since "does this path exist" is always answerable.
func readFileOrPlistValue(path, plistKey string) (string, error) {
	path = strings.TrimSpace(path)
	plistKey = strings.TrimSpace(plistKey)
	if path == "" {
		return "", fmt.Errorf("a file or plist path is required")
	}
	if plistKey == "" {
		_, err := os.Stat(path)
		return fmt.Sprintf("%t", err == nil), nil
	}
	out, err := exec.Command("plutil", "-extract", plistKey, "raw", "-o", "-", path).Output()
	if err != nil {
		return "", fmt.Errorf("reading key %q from %s: %v", plistKey, path, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// findInstalledAppVersion reuses the same Info.plist scan apps_macos.go's
// GetInstalledApps() already builds for app-inventory reporting — no
// separate lookup path to keep in sync. Bundle identifiers are compared
// case-insensitively for robustness (real-world casing is inconsistent).
func findInstalledAppVersion(identifier string) (string, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return "", fmt.Errorf("no bundle identifier configured")
	}
	target := strings.ToLower(identifier)
	for _, app := range GetInstalledApps() {
		if strings.ToLower(app.Identifier) == target {
			if app.Version != "" {
				return app.Version, nil
			}
			return "installed", nil
		}
	}
	return "", fmt.Errorf("app %q not found", identifier)
}

// runCustomCommand is the "advanced" checker type — see this repo's README
// and Settings > Custom Device Checks' UI warning: it runs exactly what the
// admin entered, with no sandboxing beyond the agent's own process
// privileges (this agent runs as root via LaunchDaemon). 30s timeout,
// output capped at 4KB before being reported.
func runCustomCommand(command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", fmt.Errorf("no command configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/bin/bash", "-c", command)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()

	output := strings.TrimSpace(out.String())
	if len(output) > 4000 {
		output = output[:4000] + "… (truncated)"
	}

	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("command timed out after 30s")
	}
	if err != nil {
		if output != "" {
			return "", fmt.Errorf("command exited with error: %v — output: %s", err, output)
		}
		return "", fmt.Errorf("command exited with error: %v", err)
	}
	return output, nil
}
