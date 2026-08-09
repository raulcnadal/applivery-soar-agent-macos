# Applivery SOAR Agent — macOS

A lightweight, native Go background service for macOS devices. It collects
security posture, device telemetry, and (optionally) software inventory and
custom admin-defined checks, then reports them to an
[Applivery SOAR](https://github.com/raulcnadal/applivery-soar) instance,
where they become available as **Compliance Policy** conditions and
Overview/Devices telemetry.

The agent compiles as a universal (arm64 + amd64) binary and ships inside a
`.pkg` installer that deploys it as `/usr/local/bin/applivery-soar-agent`,
running persistently under `launchd` as the `es.mi-labs.soar.agent`
LaunchDaemon (root).

---

## Getting the binary

You don't need to build this yourself, and you don't need a GitHub token —
the compiled `.pkg` is downloadable straight from your SOAR instance:

**Settings → Device Data Webhook → Applivery SOAR Agent**, click **Download**
next to macOS. This app's own CI publishes a fresh universal `.pkg` there on
every push to `main`, mirrored into the SOAR backend itself (no GitHub
authentication needed, same as pulling a public Docker image). The same
Settings page also has a **Publish to Applivery** button that uploads the
binary straight into your Applivery organization's App Distribution, so it
can be assigned to Policies like any other managed app.

This repo's raw GitHub Releases remain available as a fallback via the
optional GitHub-token path further down the same Settings panel, for anyone
who already configured it.

---

## Architecture

* **Native Go, universal binary:** built separately for `arm64` and `amd64`
  and combined with `lipo` into one binary that runs on both Apple Silicon
  and Intel Macs — no per-architecture package needed.
* **Managed Configuration:** no endpoint or secret is baked into the binary.
  At startup it reads `/Library/Preferences/es.mi-labs.soar.agent.json`,
  which any MDM — Applivery itself via a Custom Settings payload, Jamf,
  Intune, etc. — can deploy as a managed preference file.
* **LaunchDaemon:** runs persistently as root, starts on boot, and is
  restarted automatically by `launchd` if it exits (`KeepAlive`).
* **Reporting loop:** wakes on a configurable timer (`interval_sec`, default
  1 hour — 3600s), gathers telemetry, and POSTs it with retry + backoff.
* **Custom Device Checks:** once per cycle, before reporting, the agent
  polls the backend for whatever checks an admin has defined for macOS in
  **Settings → Custom Device Checks**, runs each one locally, and includes
  the results in the same report — no separate call, no local state kept
  between cycles. A check created or edited in the dashboard takes effect on
  this device's very next report.

---

## Configuration Reference (Managed Configuration)

All values live in `/Library/Preferences/es.mi-labs.soar.agent.json`. There
is no compiled-in default for `workspace_slug` or `report_secret` — until
both are set, the agent logs a warning each cycle and reports nothing.

| Key | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `base_url` | String | `https://soar.mi-labs.es` | Base URL of your Applivery SOAR instance. |
| `workspace_slug` | String | *(none — required)* | Your workspace identifier. |
| `report_secret` | String | *(none — required)* | Device-report webhook secret (Settings → Device Data Webhook → Generate webhook secret). |
| `interval_sec` | Integer | `3600` | Reporting interval in seconds (values under 30 fall back to the default). |
| `report_bitlocker` | Boolean | `true` | Include FileVault disk-encryption status. (Same JSON key name as the Windows agent's BitLocker toggle, for a shared Managed Configuration template — on macOS it controls FileVault.) |
| `report_firewall` | Boolean | `true` | Include Application Firewall status. |
| `report_apps` | Boolean | `false` | Include the full installed-application inventory. |

Settings → Device Data Webhook generates a ready-to-deploy `.json` file with
all of these pre-filled for your workspace — you shouldn't need to type any
of this by hand.

### Example

```json
{
  "base_url": "https://soar.mi-labs.es",
  "workspace_slug": "your-workspace",
  "report_secret": "<generated in Settings>",
  "interval_sec": 3600,
  "report_bitlocker": true,
  "report_firewall": true,
  "report_apps": true
}
```

---

## Telemetry & Data Collection

Read natively via system commands (`sw_vers`, `fdesetup`,
`socketfilterfw`, `sysadminctl`, `profiles`, `df`, `sysctl`) — no bundled
shell scripts:

1. **Device identity** — hardware serial via `ioreg -c IOPlatformExpertDevice`.
2. **OS build** — `sw_vers -buildVersion`.
3. **FileVault status** (when `report_bitlocker=true`) — `fdesetup status`.
4. **Firewall status** (when `report_firewall=true`) — Application Firewall global state.
5. **XProtect** — always reported `true` (XProtect ships enabled by default on all supported macOS versions; this agent doesn't attempt to detect tampering).
6. **Secure Token / Screen Lock** — resolved against whoever's actually logged in at the console (`stat -f%Su /dev/console`, excluding `root`/empty); a machine with nobody logged in at the console reports both as unknown/false rather than guessed, since these are per-user settings, not machine-wide.
7. **MDM enrollment** — `profiles status -type enrollment`.
8. **Disk free/used, uptime** — `df` and `sysctl kern.boottime`.
9. **Installed software** (when `report_apps=true`) — scans `/Applications`,
   `/System/Applications`, and `~/Applications`, reading real
   `CFBundleIdentifier`/`CFBundleShortVersionString` from each app's
   `Info.plist`.

A serial number that's empty or `UNKNOWN` is treated as unusable — the agent
skips that cycle's report instead of risking two different degenerate-serial
Macs silently overwriting each other's data on the backend.

---

## Custom Device Checks

Beyond the fixed telemetry above, an admin can define arbitrary checks in
**Settings → Custom Device Checks** that this agent runs locally every
cycle. Each check has a `checkerType` and its own `params`. The structure
mirrors the Windows agent's checks 1:1 — only the underlying OS calls
differ:

| Checker type | What it does | macOS implementation |
| :--- | :--- | :--- |
| `processRunning` | Is a named process currently running? | `pgrep -ix <name>` |
| `serviceStatus` | Is a named launchd job currently running? | `launchctl list` output parsed for the job's label; PID `-` means loaded but not running |
| `registryOrFileValue` | Read a plist key, or just check a path exists | With a `plistKey`: `plutil -extract <key> raw -o - <path>`. Without one: a plain existence check via `os.Stat` |
| `appInstalled` | Is an app installed, and what version? | Reuses the same `Info.plist` scan as `report_apps` |
| `command` | Run an arbitrary shell command and report its output | `/bin/bash -c "<command>"` as root, 30s timeout, output capped at 4KB |

A check failing to *run* (launchd job not loaded, plist key missing, command
timeout) is reported as an **error**, which the backend's compliance
evaluator treats the same as "no data yet." A legitimately negative result —
a process that simply isn't running — is a normal value, not an error. The
`command` checker type runs exactly what's typed into the dashboard as root
(this agent's own LaunchDaemon privilege) with no sandboxing — Settings
surfaces an explicit warning about this; use it deliberately.

---

## Webhook Endpoints & Payload Structure

* **Reports:** `POST <base_url>/api/device-data/report` and, when
  `report_apps=true`, `POST <base_url>/api/device-data/report-apps`.
* **Custom check definitions poll:** `GET <base_url>/api/device-data/custom-checks?platform=macos`.
* **Headers on every request:**
  * `Content-Type: application/json` (report calls only)
  * `X-Workspace-Slug: <workspace_slug>`
  * `X-Device-Report-Secret: <report_secret>`

### Device report payload

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
    "DiskFreeGb": 245,
    "DiskUsedPercent": 61,
    "UptimeDays": 14
  },
  "customCheckResults": {
    "edr-running": { "value": true },
    "screen-lock-timeout": { "error": "reading key ... : exit status 1" }
  }
}
```

`customCheckResults` is omitted entirely (not sent as an empty object) when
no checks are configured, so the backend keeps whatever it already had
rather than wiping it.

### App inventory payload (`report_apps=true`)

```json
{
  "platform": "macos",
  "serialNumber": "C02XYZ123ABC",
  "apps": [
    { "identifier": "com.google.Chrome", "name": "Google Chrome", "version": "125.0.6422.113" }
  ]
}
```

---

## Building From Source

```bash
git clone https://github.com/raulcnadal/applivery-soar-agent-macos.git
cd applivery-soar-agent-macos

CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o build/agent-arm64 .
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o build/agent-amd64 .
lipo -create -output applivery-soar-agent build/agent-arm64 build/agent-amd64
```

`.github/workflows/build-pkg.yml` does exactly this, then wraps the
universal binary and `es.mi-labs.soar.agent.plist` into
`Applivery-SOAR-Agent.pkg` via `pkgbuild`, and — on pushes to `main` — also
publishes the `.pkg` to the SOAR backend's zero-config download endpoint
(gated by the `SOAR_AGENT_BUILD_SECRET` repository secret) and republishes a
rolling `latest` GitHub Release.

---

## Deployment via MDM

1. **Upload** `Applivery-SOAR-Agent.pkg` to your MDM as a package/PKG app.
2. **Push Managed Configuration** — deploy the `.json` file from Settings →
   Device Data Webhook to `/Library/Preferences/es.mi-labs.soar.agent.json`
   via your MDM's Custom Settings mechanism.
3. **Assign** the package and configuration profile to your target device
   groups. The package's postinstall script sets root ownership/permissions
   on the binary and plist, then bootstraps the LaunchDaemon immediately —
   no reboot required.

### Manual installation (no MDM)

```bash
sudo installer -pkg Applivery-SOAR-Agent.pkg -target /
sudo mkdir -p /Library/Preferences
sudo tee /Library/Preferences/es.mi-labs.soar.agent.json > /dev/null <<'EOF'
{
  "base_url": "https://soar.mi-labs.es",
  "workspace_slug": "your-workspace",
  "report_secret": "<generated in Settings>",
  "interval_sec": 3600,
  "report_bitlocker": true,
  "report_firewall": true,
  "report_apps": true
}
EOF
sudo chmod 644 /Library/Preferences/es.mi-labs.soar.agent.json
sudo launchctl bootout system/es.mi-labs.soar.agent 2>/dev/null || true
sudo launchctl bootstrap system /Library/LaunchDaemons/es.mi-labs.soar.agent.plist
```

---

## Troubleshooting

* **Service status:**

  ```bash
  sudo launchctl list | grep es.mi-labs.soar.agent
  ```

* **Logs:**

  ```bash
  tail -f /var/log/applivery-soar-agent.log
  tail -f /var/log/applivery-soar-agent.error.log
  ```

* **Connectivity:** confirm outbound HTTPS to your SOAR instance's `base_url`
  is permitted through any local firewall or proxy.
* **"No WorkspaceSlug/ReportSecret" in the logs:** the managed preference
  file hasn't been deployed yet, or is missing `workspace_slug`/
  `report_secret` — see *Configuration Reference* above.
