//go:build !windows
// +build !windows

package main

import (
	"bytes"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// GatherSecurityAttributes executes local macOS diagnostics to populate security telemetry
// matching the behavior of report-security-attributes.sh
func GatherSecurityAttributes() map[string]interface{} {
	attrs := make(map[string]interface{})

	attrs["FileVaultEnabled"] = GetFileVaultStatus()
	attrs["FirewallEnabled"] = GetFirewallStatus()
	attrs["XProtectEnabled"] = GetXProtectStatus()
	attrs["SecureTokenEnabled"] = GetSecureTokenStatus()
	attrs["ScreenLockEnabled"] = GetScreenLockStatus()
	attrs["MdmEnrolled"] = GetMdmEnrollmentStatus()
	attrs["OsBuildNumber"] = GetOSBuild()

	freeGb, usedPct := GetDiskMetrics()
	attrs["DiskFreeGb"] = freeGb
	attrs["DiskUsedPercent"] = usedPct
	attrs["UptimeDays"] = GetUptimeDays()

	return attrs
}

func GetFileVaultStatus() bool {
	cmd := exec.Command("fdesetup", "status")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	outputStr := string(out)
	return strings.Contains(outputStr, "FileVault is On.")
}

func GetFirewallStatus() bool {
	// Check via socketfilterfw
	cmd := exec.Command("/usr/libexec/ApplicationFirewall/socketfilterfw", "--getglobalstate")
	out, err := cmd.Output()
	if err != nil {
		// Fallback to defaults read
		cmdFallback := exec.Command("defaults", "read", "/Library/Preferences/com.apple.alf", "globalstate")
		outFallback, errFB := cmdFallback.Output()
		if errFB != nil {
			return false
		}
		val := strings.TrimSpace(string(outFallback))
		return val == "1" || val == "2"
	}
	return strings.Contains(string(out), "State = 1") || strings.Contains(string(out), "State = 2")
}

func GetXProtectStatus() bool {
	// XProtect bundle check paths across modern macOS versions
	paths := []string{
		"/Library/Apple/System/Library/CoreServices/XProtect.bundle",
		"/System/Library/CoreServices/CoreTypes.bundle/Contents/Resources/XProtect.bundle",
	}
	for _, p := range paths {
		if pathExists(p) {
			return true
		}
	}
	return false
}

func pathExists(path string) bool {
	cmd := exec.Command("test", "-d", path)
	return cmd.Run() == nil
}

func GetSecureTokenStatus() bool {
	// Check if the current console user has a secure token
	currentUser := getCurrentConsoleUser()
	if currentUser == "" || currentUser == "root" {
		return false
	}
	cmd := exec.Command("sysadminctl", "-secureTokenStatus", currentUser)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "ENABLED")
}

func getCurrentConsoleUser() string {
	cmd := exec.Command("stat", "-f", "%Su", "/dev/console")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func GetScreenLockStatus() bool {
	cmd := exec.Command("defaults", "read", "com.apple.screensaver", "askForPassword")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	val := strings.TrimSpace(string(out))
	return val == "1"
}

func GetMdmEnrollmentStatus() bool {
	cmd := exec.Command("profiles", "status", "-type", "enrollment")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "Enrolled via MDM: Yes")
}

func GetDiskMetrics() (int64, int) {
	var stat syscall.Statfs_t
	err := syscall.Statfs("/", &stat)
	if err != nil {
		return 0, 0
	}

	totalBytes := int64(stat.Blocks) * int64(stat.Bsize)
	freeBytes := int64(stat.Bavail) * int64(stat.Bsize)
	usedBytes := totalBytes - int64(stat.Bfree)*int64(stat.Bsize)

	freeGb := freeBytes / (1024 * 1024 * 1024)
	var usedPct int
	if totalBytes > 0 {
		usedPct = int((float64(usedBytes) / float64(totalBytes)) * 100)
	}

	return freeGb, usedPct
}

func GetUptimeDays() int {
	cmd := exec.Command("sysctl", "-n", "kern.boottime")
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	// Output format: { sec = 1718000000, usec = 0 } ...
	outputStr := string(out)
	var bootSec int64
	if idx := strings.Index(outputStr, "sec = "); idx != -1 {
		parts := strings.Split(outputStr[idx+6:], ",")
		if len(parts) > 0 {
			bootSec, _ = strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
		}
	}

	if bootSec == 0 {
		return 0
	}

	nowSec := time.Now().Unix()
	diffSec := nowSec - bootSec
	if diffSec < 0 {
		return 0
	}

	return int(diffSec / 86400)
}