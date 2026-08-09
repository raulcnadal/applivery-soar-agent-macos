# Applivery SOAR Agent — macOS

The **Applivery SOAR Agent for macOS** is a lightweight, background telemetry and asset reporting service written in Go. Designed for enterprise environments, it runs as a native macOS LaunchDaemon to gather system security posture, device metrics, and application inventories, reporting them securely to the Applivery SOAR platform on a scheduled interval.

---

## Architecture & Features

* **Native Go Implementation:** Compiled directly for macOS (`amd64` and `arm64`), featuring zero runtime dependencies outside standard libraries.
* **Security Posture Attestation:** Automatically evaluates critical local macOS security parameters:
* FileVault disk encryption status
* Application firewall state
* XProtect security agent monitoring
* Secure Token status and Screen Lock enforcement
* MDM enrollment verification
* OS Build version and disk capacity metrics


* **Application Inventory Reporting:** Scans standard system and user paths (`/Applications`, `/System/Applications`, `~/Applications`), reading real `CFBundleIdentifier` values and version info directly from each app's `Info.plist`.
* **Managed Configuration:** Supports enterprise-wide configuration deployment via standard plist or JSON configuration profiles under `/Library/Preferences/es.mi-labs.soar.agent.json`.
* **LaunchDaemon Integration:** Runs persistently in the background under `launchd`, ensuring automated start on boot and graceful handling of system lifecycle events.

---

## Configuration

The agent reads its operational configuration from a managed preference file located at:

```text
/Library/Preferences/es.mi-labs.soar.agent.json

```

### Configuration Parameters

| Parameter | Type | Default | Description |
| --- | --- | --- | --- |
| `base_url` | String | `https://soar.mi-labs.es` | Base URL of the Applivery SOAR backend instance. |
| `workspace_slug` | String | `friendly-emporium` | Target workspace identifier for webhook ingestion. |
| `report_secret` | String | `db4rLzdlJBo08SArnnH9pHZm` | Secret authentication token for API authorization. |
| `interval_sec` | Integer | `3600` | Frequency of telemetry gathering and reporting in seconds (minimum 30s). |
| `report_filevault` | Boolean | `true` | Include FileVault encryption telemetry. |
| `report_firewall` | Boolean | `true` | Include firewall status telemetry. |
| `report_apps` | Boolean | `false` | Include installed application inventory reports. |

### Example Configuration File

```json
{
  "base_url": "https://soar.mi-labs.es",
  "workspace_slug": "friendly-emporium",
  "report_secret": "db4rLzdlJBo08SArnnH9pHZm",
  "interval_sec": 3600,
  "report_filevault": true,
  "report_firewall": true,
  "report_apps": true
}

```

---

## Webhook Endpoint & Payload Structure

The agent sends periodic payloads to the Applivery SOAR ingestion endpoint:

* **Method:** `POST`
* **URL:** `https://soar.mi-labs.es/api/device-data/report`
* **Headers:**
* `Content-Type: application/json`
* `X-Workspace-Slug: <workspace_slug>`
* `X-Device-Report-Secret: <report_secret>`



### 1. Device Security & Telemetry Payload Example

```json
{
  "platform": "macos",
  "serialNumber": "C02XYZ123ABC",
  "attributes": {
    "FileVaultEnabled": true,
    "FirewallEnabled": true,
    "XProtectEnabled": true,
    "SecureTokenEnabled": true,
    "ScreenLockEnabled": true,
    "MdmEnrolled": true,
    "OsBuildNumber": "23F79",
    "DiskFreeGb": 245.5,
    "DiskUsedPercent": 61.2,
    "UptimeDays": 14
  }
}

```

### 2. Installed Applications Payload Example

When `report_apps` is enabled, the agent sends an application inventory batch:

```json
{
  "platform": "macos",
  "serialNumber": "C02XYZ123ABC",
  "apps": [
    {
      "identifier": "com.google.Chrome",
      "name": "Google Chrome",
      "version": "125.0.6422.113"
    },
    {
      "identifier": "com.microsoft.teams2",
      "name": "Microsoft Teams",
      "version": "24124.1415.2872.4300"
    }
  ]
}

```

---

## Deployment & Installation Guide

### 1. Building the Binary

Compile the Go application natively on macOS (or cross-compile from a build server):

```bash
# Clone the repository
git clone https://github.com/raulcnadal/applivery-soar-agent-macos.git
cd applivery-soar-agent-macos

# Build optimized binary
CGO_ENABLED=0 GOOS=darwin go build -ldflags="-s -w" -o Applivery-SOAR-Agent

```

### 2. Service Definition (LaunchDaemon plist)

Create or deploy the LaunchDaemon configuration file at `/Library/LaunchDaemons/es.mi-labs.soar.agent.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>es.mi-labs.soar.agent</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/Applivery-SOAR-Agent</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/var/log/applivery-soar-agent.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/applivery-soar-agent.error.log</string>
</dict>
</plist>

```

### 3. Installation Script

Execute the following setup sequence with administrative privileges to install the binary, set permissions, register configuration, and start the background service:

```bash
# 1. Copy binary to destination
sudo mkdir -p /usr/local/bin
sudo cp Applivery-SOAR-Agent /usr/local/bin/Applivery-SOAR-Agent
sudo chmod 755 /usr/local/bin/Applivery-SOAR-Agent

# 2. Deploy Managed Configuration
sudo mkdir -p /Library/Preferences
sudo tee /Library/Preferences/es.mi-labs.soar.agent.json > /dev/null <<EOF
{
  "base_url": "https://soar.mi-labs.es",
  "workspace_slug": "friendly-emporium",
  "report_secret": "db4rLzdlJBo08SArnnH9pHZm",
  "interval_sec": 3600,
  "report_filevault": true,
  "report_firewall": true,
  "report_apps": true
}
EOF
sudo chmod 644 /Library/Preferences/es.mi-labs.soar.agent.json

# 3. Register and Load LaunchDaemon
sudo cp es.mi-labs.soar.agent.plist /Library/LaunchDaemons/es.mi-labs.soar.agent.plist
sudo chmod 644 /Library/LaunchDaemons/es.mi-labs.soar.agent.plist
sudo launchctl bootout system/es.mi-labs.soar.agent 2>/dev/null || true
sudo launchctl bootstrap system /Library/LaunchDaemons/es.mi-labs.soar.agent.plist

```

---

## Troubleshooting

* **Verify Service Status:**
```bash
sudo launchctl list | grep es.mi-labs.soar.agent

```


* **Inspect Agent Logs:**
```bash
tail -f /var/log/applivery-soar-agent.log
tail -f /var/log/applivery-soar-agent.error.log

```


* **Test Connectivity Manually:**
Ensure outbound HTTPS access to `https://soar.mi-labs.es` is permitted through any local firewalls or web proxies.