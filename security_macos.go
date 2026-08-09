//go:build !windows
// +build !windows

package main

import (
	"os/exec"
	"strings"
)

func GetSerialNumber() string {
	out, err := exec.Command("ioreg", "-c", "IOPlatformExpertDevice", "-d", "2").Output()
	if err != nil {
		return "UNKNOWN"
	}
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		if strings.Contains(line, "IOPlatformSerialNumber") {
			parts := strings.Split(line, "\"")
			if len(parts) >= 4 {
				return parts[3]
			}
		}
	}
	return "UNKNOWN"
}

func GetOSBuild() string {
	out, err := exec.Command("sw_vers", "-buildVersion").Output()
	if err != nil {
		return "Unknown"
	}
	return strings.TrimSpace(string(out))
}

func GetFileVaultStatus() bool {
	out, err := exec.Command("fdesetup", "status").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "FileVault is On.")
}

func GetFirewallStatus() bool {
	out, err := exec.Command("/usr/libexec/ApplicationFirewall/socketfilterfw", "--getglobalstate").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "State = enabled")
}

func GetXProtectStatus() bool {
	// XProtect is built-in and active on macOS
	return true
}

func GetSecureTokenStatus() bool {
	out, err := exec.Command("sysadminctl", "-secureTokenStatus", "root").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "ENABLED")
}

func GetScreenLockStatus() bool {
	out, err := exec.Command("defaults", "read", "com.apple.screensaver", "askForPassword").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "1"
}

func GetMdmEnrolledStatus() bool {
	out, err := exec.Command("profiles", "status", "-type", "enrollment").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "Enrolled via DEP: Yes") || strings.Contains(string(out), "MDM enrollment: Yes")
}