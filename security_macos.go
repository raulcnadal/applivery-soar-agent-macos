//go:build !windows
// +build !windows

package main

import (
	"os/exec"
	"strconv"
	"strings"
	"time"
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

func GetDiskFreeGB() int64 {
	out, err := exec.Command("df", "-g", "/").Output()
	if err != nil {
		return 0
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) >= 2 {
		fields := strings.Fields(lines[1])
		if len(fields) >= 4 {
			if val, err := strconv.ParseInt(fields[3], 10, 64); err == nil {
				return val
			}
		}
	}
	return 0
}

func GetDiskUsedPercent() int {
	out, err := exec.Command("df", "-h", "/").Output()
	if err != nil {
		return 0
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) >= 2 {
		fields := strings.Fields(lines[1])
		if len(fields) >= 5 {
			pctStr := strings.TrimSuffix(fields[4], "%")
			if val, err := strconv.Atoi(pctStr); err == nil {
				return val
			}
		}
	}
	return 0
}

func GetUptimeDays() int {
	out, err := exec.Command("sysctl", "-n", "kern.boottime").Output()
	if err != nil {
		return 0
	}
	str := string(out)
	if idx := strings.Index(str, "sec = "); idx != -1 {
		rest := str[idx+6:]
		if commaIdx := strings.Index(rest, ","); commaIdx != -1 {
			secStr := strings.TrimSpace(rest[:commaIdx])
			if bootSec, err := strconv.ParseInt(secStr, 10, 64); err == nil {
				nowSec := time.Now().Unix()
				if nowSec > bootSec {
					return int((nowSec - bootSec) / 86400)
				}
			}
		}
	}
	return 0
}

func GatherSecurityAttributes(config Config) map[string]interface{} {
	attributes := make(map[string]interface{})

	if config.ReportBitLocker {
		attributes["FileVaultEnabled"] = GetFileVaultStatus()
	}
	if config.ReportFirewall {
		attributes["FirewallEnabled"] = GetFirewallStatus()
	}

	attributes["XProtectEnabled"] = GetXProtectStatus()
	attributes["SecureTokenEnabled"] = GetSecureTokenStatus()
	attributes["ScreenLockEnabled"] = GetScreenLockStatus()
	attributes["MdmEnrolled"] = GetMdmEnrolledStatus()
	attributes["OsBuildNumber"] = GetOSBuild()
	attributes["DiskFreeGb"] = GetDiskFreeGB()
	attributes["DiskUsedPercent"] = GetDiskUsedPercent()
	attributes["UptimeDays"] = GetUptimeDays()

	return attributes
}