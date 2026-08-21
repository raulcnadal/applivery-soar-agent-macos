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

**Settings → Applivery SOAR Agent**, click **Download**
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
  restarted automatically by `launchd` if it exits (`KeepAlive`) — see
  [Process Supervision](#process-supervision) below for why this doesn't
  need anything more than that.
* **Reporting loop:** wakes on a configurable timer (`interval_sec`, default
  1 hour — 3600s), gathers telemetry, and POSTs it with retry + backoff.
* **Custom Device Checks:** once per cycle, before reporting, the agent
  polls the backend for whatever checks an admin has defined for macOS in
  **Settings → Custom Device Checks**, runs each one locally, and includes
  the results in the same report — no separate call, no local state kept
  between cycles. A check created or edited in the dashboard takes effect on
  this device's very next report.
* **mTLS Agent Authentication (optional, per workspace):** once a
  `bootstrap_token` is configured, this device registers a per-device
  client certificate and authenticates every subsequent request with it
  instead of the shared `report_secret`, renewing itself automatically —
  see [mTLS Agent Authentication](#mtls-agent-authentication) below.
* **Menu bar app:** a separate SwiftUI app (`Applivery SOAR.app`) shows this
  device's live status — compliance, what was last reported, Force
  report/Force evaluate buttons — and posts a notification when compliance
  changes, all backed by a small file-based IPC contract with this daemon —
  see [Menu Bar App](#menu-bar-app) below.
* **Event Watches (optional, per workspace):** once an admin defines a watch
  in **Settings → Event-Driven Detection**, this agent monitors that signal
  continuously between report cycles — a file/folder change or a launchd
  job's state — instead of waiting for the next scheduled poll, see [Event
  Watches](#event-watches) below.

---

## Process Supervision

The Windows agent needs a second, independent Windows Service (a "mutual
watchdog") purely because Windows' Service Control Manager does not restart
a killed service on its own by default. **This agent doesn't need that —
`launchd` already does the job natively**, so there's deliberately no
second supervisor process here, and no plan to add one:

* `KeepAlive` (plain boolean `true`, not the `{SuccessfulExit: false}`
  dictionary form) tells `launchd` to relaunch this daemon after *any* exit —
  a crash, a `kill -9`, or even a clean zero-status exit — not just an
  abnormal one. This alone is the equivalent of everything the Windows
  agent's `AppliverySOARWatchdog` service exists to provide.
* `ThrottleInterval` (10s) caps how fast `launchd` will restart a job stuck
  in a crash loop, so a bad Managed Configuration or a genuine bug can't turn
  into a restart storm hammering this device or the SOAR backend.
* **Honest limitation, not a new promise:** same as the Windows agent's own
  README already discloses about its watchdog ("a deterrent, not a hard
  guarantee"), a local admin can always defeat this — `sudo launchctl
  bootout system/es.mi-labs.soar.agent` unloads the daemon, and `launchd`
  will *not* re-bootstrap it on its own until the next boot or an explicit
  `launchctl bootstrap`. `KeepAlive` protects against the daemon dying on
  its own; it was never going to protect against someone with root
  deliberately telling `launchd` to stop supervising it, on either platform.
* **The menu bar app gets the same treatment:** its
  own LaunchAgent plist (`es.mi-labs.soar.menubar.plist`) sets `KeepAlive`/
  `ThrottleInterval` identically to this daemon's own plist — see [Menu Bar
  App](#menu-bar-app) below for the full design.
* **Still deliberately not built:** this daemon does not yet cross-check
  that the menu bar app's LaunchAgent is actually loaded for whichever user
  is at the console and re-bootstrap it if not (e.g. `launchctl print
  gui/<uid>/...`, re-running `launchctl bootstrap` if missing). The
  installer's postinstall script bootstraps it once at install time for
  whoever's logged in then (see [Menu Bar App](#menu-bar-app)), and
  `RunAtLoad` covers every login after that — the one gap is a user who was
  already logged in during a Managed-Configuration-only push that doesn't
  reinstall the package. Flagging this explicitly rather than leaving it
  silently unbuilt; low priority since the LaunchAgent itself is installed
  once and rarely removed by anything other than a full uninstall.

---

## Configuration Reference (Managed Configuration)

All values live in `/Library/Preferences/es.mi-labs.soar.agent.json`. There
is no compiled-in default for `workspace_slug` — until it plus either
`report_secret` or `bootstrap_token` are set, the agent logs a warning each
cycle and reports nothing.

| Key | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `base_url` | String | `https://soar.mi-labs.es` | Base URL used for reporting (report, report-apps, custom-checks, agent-status) AND certificate renewal. Once a workspace uses mTLS, this must point at the dedicated agent subdomain (Settings → mTLS Agent Authentication → Reverse Proxy Configuration) — renewal always requires a valid client certificate, so it must go through that vhost. |
| `register_url` | String | *(none — optional, falls back to `base_url`)* | Base URL used ONLY for the one-time `/api/device-mtls/register` call. `/register` never presents a client cert (the bootstrap token is the credential), so it doesn't need the mTLS vhost's health at all — setting this to the ordinary dashboard domain decouples first-time enrollment from whether that vhost happens to be up. Leave unset for the historical single-URL behavior. |
| `workspace_slug` | String | *(none — required)* | Your workspace identifier. |
| `report_secret` | String | *(none — optional)* | Device-report webhook secret (Settings → Applivery SOAR Agent → Generate webhook secret). Either this or `bootstrap_token` must be set — an mTLS-only deployment (only `bootstrap_token` set) is fully supported. |
| `bootstrap_token` | String | *(none — optional)* | The workspace's Global Bootstrap Token (Settings → mTLS Agent Authentication → Generate). The SAME value is pushed to every device in the fleet — see [mTLS Agent Authentication](#mtls-agent-authentication) below. Safe to leave unset if your workspace hasn't enabled mTLS yet. |
| `interval_sec` | Integer | `3600` | Reporting interval in seconds (values under 30 fall back to the default). |
| `report_filevault` | Boolean | `true` | Include FileVault disk-encryption status. |
| `report_firewall` | Boolean | `true` | Include Application Firewall status. |
| `report_apps` | Boolean | `false` | Include the full installed-application inventory. |

Settings → Applivery SOAR Agent generates a ready-to-deploy `.json` file with
all of these pre-filled for your workspace (including `bootstrap_token`, if
one is configured) — you shouldn't need to type any of this by hand.

### Example

```json
{
  "base_url": "https://soar.mi-labs.es",
  "workspace_slug": "your-workspace",
  "report_secret": "<generated in Settings>",
  "bootstrap_token": "<optional — Settings → mTLS Agent Authentication>",
  "interval_sec": 3600,
  "report_filevault": true,
  "report_firewall": true,
  "report_apps": true
}
```

---

## mTLS Agent Authentication

The agent can authenticate to SOAR with a per-device client certificate
instead of the shared `report_secret`. This is opt-in per workspace;
nothing changes for a device that never receives a `bootstrap_token`.
Client logic and endpoints are identical to the Windows agent's own mTLS
support — only the keystore location differs.

Registration uses a single **Global Bootstrap Token**: one value, the SAME
on every device in the fleet, pushed via one Managed Configuration policy —
not a per-device or one-time credential. A device proves it's allowed to
register with that token PLUS a live check (done server-side) that its own
serial number is currently a known, enrolled device in this workspace's
Applivery UEM fleet. Only devices Applivery already knows about can ever
register.

1. **First run with `bootstrap_token` set:** the agent generates an ECDSA
   P-256 keypair locally (the private key never leaves the device), builds a
   CSR, and registers with the backend over plain HTTPS using the token —
   against `register_url` if set, otherwise `base_url`. The backend
   validates the token, checks the device's serial number against
   Applivery's live fleet, and — if both check out — issues a certificate
   immediately (no admin approval step; a bootstrap token is unattended by
   design). The issued certificate + key are stored under
   `/Library/Application Support/Applivery/SOAR/mtls/`, root-owned (`0700`
   directory, `0600` files) — since this agent already runs as root via
   LaunchDaemon, plain Unix permissions already are the access-control
   boundary here, no separate ACL tool needed (unlike Windows, which shells
   out to `icacls` to achieve the same restriction). This is a file-based
   keystore, not the real macOS Keychain — a deliberate simplification, not
   an oversight. A device that already has an active certificate can
   never be silently re-registered this way — the backend rejects it — so
   leaving `bootstrap_token` in place after enrollment is harmless.
2. **Every report cycle afterward:** if a valid certificate is loaded, ALL
   requests to the backend (reports, custom-checks poll) present it via
   mTLS instead of sending `X-Device-Report-Secret` — the two auth modes
   are never mixed on the same request.
3. **Renewal is automatic and silent:** once less than a third of the
   certificate's total validity window remains, the agent generates a fresh
   keypair+CSR and renews using its current (still-valid) certificate to
   authenticate the renewal call — no bootstrap token is ever needed again
   after the first successful registration.
4. **If registration/renewal fails** (backend unreachable, no CA configured
   yet, token wrong, serial number not yet visible to Applivery), the agent
   just keeps using whatever auth it already has (the legacy secret, or its
   current not-yet-expired certificate) and retries on the next report
   cycle — never blocks or fails a report because of this.

**Reverse proxy**: the proxy in front of the backend must terminate the mTLS
handshake and forward the verified client identity via headers — Settings →
mTLS Agent Authentication shows the exact nginx/NPM config (and the
equivalent for Traefik/Caddy/HAProxy) plus whether the internal proxy secret
is currently configured on this backend.

---

## Menu Bar App

`MenuBarApp/` is a separate SwiftUI app — `Applivery SOAR.app` — that shows
this device's live status from the menu bar: device name, a Compliant/issue
pill, Force report / Force evaluate compliance buttons, what was last
reported (FileVault, Firewall, app inventory), and — when compliance is
available — a risk score bar and the full per-policy pass/fail list. It's
the direct macOS counterpart of the Windows agent's tray icon and status
card, built to the identical visual/data design (same colors, same risk-tier
thresholds, same 6-policy-row cap with a "+N more" line, same "Managed by
{slug}" footer) — see that repo's own `tray/card.go` if you're comparing the
two side by side.

**Why a separate Swift Package instead of an `.xcodeproj`:** `MenuBarApp/`
is a plain Swift Package (`Package.swift` + `Sources/`), not a hand-authored
Xcode project file. An `.xcodeproj`/`.pbxproj` is a large, mostly-opaque
format that's painful to review as a diff and easy to corrupt editing by
hand outside Xcode itself; a Swift Package is just source files, reviewable
the same way as every `.go` file in this repo. `.github/workflows/
build-pkg.yml` runs `swift build` on the `macos-latest` runner (which has a
full Xcode/Swift toolchain) as this target's actual compile verification —
same role `go build` plays for the daemon.

### IPC contract with the daemon

Both processes share one directory: `/Library/Application Support/Applivery/
SOAR/`. The daemon (root, via LaunchDaemon) owns writing to it; this app
(unprivileged, via a per-console-user LaunchAgent) only reads from it and
creates two small trigger files — see `agentstatus_macos.go` (repo root) for
the daemon's full implementation and exactly why the directory ends up
`chmod 1777` (world-writable + sticky, like `/tmp`) after every daemon
write.

| File | Written by | Read/consumed by | Purpose |
| :--- | :--- | :--- | :--- |
| `status.json` | Daemon, after every report cycle and forced evaluation | This app, on open + a 60s timer | Everything the card renders — device name, compliance, what was last reported. Missing or unparseable is treated as "no data yet," not an error — this app can easily start before the daemon's first cycle completes. |
| `trigger-report.flag` | This app, when "Force report" is clicked | Daemon, polled every 2s | Existence alone triggers an immediate report cycle; content (an RFC3339 timestamp) is never read, only written for a human `cat`-ing the file mid-troubleshoot. Consumed (deleted) by the daemon the instant it's seen. |
| `trigger-evaluate.flag` | This app, when "Force evaluate compliance" is clicked | Daemon, polled every 2s | Same mechanism — triggers `POST /api/device-data/evaluate-now`, then a fresh compliance re-fetch patched into `status.json`. |

### Notifications

A local notification (via `UNUserNotificationCenter`) fires only on a
strict compliance-violation-count transition — 0→N ("Compliance issue
detected") or N→0 ("Compliance restored") — never on any N→M change with
both sides nonzero, and never when the backend's compliance evaluation
itself isn't available yet. The 0-baseline is re-established silently on
every app launch (in-memory only, not persisted), so a relaunch never
re-fires a notification for a transition that already happened. Identical
semantics to the Windows tray's own `checkComplianceTransition`.

### Fonts & visuals

The same 3 static Outfit weights (Regular/SemiBold/Bold) the Windows tray
embeds are bundled as Swift Package resources and registered process-wide
via `CTFontManagerRegisterFontsForURL` at launch (`FontLoader.swift`) — see
that file's doc comment for why each weight is its own font *family* as far
as CoreText is concerned (`"Outfit Regular"`/`"Outfit SemiBold"`/`"Outfit
Bold"`), not 3 weights of one family. The menu bar icon itself is currently
a placeholder SF Symbol (`checkmark.shield`, template-rendered so AppKit
auto-inverts it for light/dark menu bars) — swapping in a real Applivery
mark only requires touching `AppDelegate.swift`'s `setUpStatusItem()`.

### Installation

Ships inside the same `Applivery-SOAR-Agent.pkg` as the daemon — the
package payload places the wrapped `.app` bundle at `/Applications/
Applivery SOAR.app` and its plist at `/Library/LaunchAgents/
es.mi-labs.soar.menubar.plist`. `/Library/LaunchAgents/` is loaded
automatically by `launchd` once per GUI login, for whichever user just
logged in — unprivileged, unlike the daemon's `/Library/LaunchDaemons/`
entry. The postinstall script also bootstraps it immediately into whoever's
*already* logged in at install time (`launchctl asuser <uid> launchctl
bootstrap gui/<uid> ...`), since a plain file copy only takes effect on the
next login otherwise.

**Signing status:** CI ad-hoc signs the wrapped `.app` (`codesign --sign -`)
— enough to launch on the same class of machine that built it, but not real
Developer ID signing or notarization. That's pending Applivery's own
Developer account team setting up a real signing pipeline, same as the
daemon binary's own signing status — this repo has never shipped a
Developer-ID-signed artifact yet.

---

## Telemetry & Data Collection

Read natively via system commands (`sw_vers`, `fdesetup`,
`socketfilterfw`, `sysadminctl`, `profiles`, `df`, `sysctl`) — no bundled
shell scripts:

1. **Device identity** — hardware serial via `ioreg -c IOPlatformExpertDevice`.
2. **OS build** — `sw_vers -buildVersion`.
3. **FileVault status** (when `report_filevault=true`) — `fdesetup status`.
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

## Event Watches

Custom Device Checks and the reporting loop both run once per `interval_sec`
cycle — fine for most posture data, but too slow for "tell me the moment
this specific thing happens." **Event Watches** (Settings →
Event-Driven Detection) fill that gap: once an admin defines one, this agent
monitors that signal continuously between report cycles and calls SOAR back
the moment its own local debounce goes quiet, instead of waiting for the
next scheduled poll. This **supplements** the report cycle, it never
replaces it — a device that's offline, or hasn't picked up a watch yet,
keeps reporting exactly as before.

Two watch types exist on macOS, both implemented in `eventwatch_macos.go`:

| Watch type | What it does | macOS implementation |
| :--- | :--- | :--- |
| `fsEventsPath` | Fires when a file or directory changes | [`fsnotify`](https://github.com/fsnotify/fsnotify) (kqueue-based, pure Go, no CGo) — watches the given `path` directly; with `recursive: true` on a directory, every subdirectory that exists when the watch starts is also added |
| `launchdJobState` | Fires when a launchd job's loaded/running state changes, including a crash-restart | Polls `launchctl list <label>` every 3s and compares the (loaded, PID, LastExitStatus) tuple against the previous poll — see below for why this is polling, not a true kernel notification |

Both watch types share the daemon's per-key debouncer (a direct port of the
Windows agent's own — see `eventwatch_macos.go`'s top-of-file doc comment):
a burst of raw filesystem events collapses into one clean notify, sent only
after the signal goes quiet for `debounceMs` (5s by default, admin-editable
1-60s per watch).

**Why `launchdJobState` polls instead of subscribing to a real notification:**
unlike Windows' `RegNotifyChangeKeyValue` (registry) or an ETW session
(process lifecycle), there is no native "tell me when a launchd job's state
changes" API reachable from pure Go without CGo. Polling every 3s is a
disclosed, deliberate trade-off — it keeps this whole agent CGo-free and
cross-compilable on any CI runner with just the Go toolchain, the same
property that made `fsnotify` the choice for `fsEventsPath` over raw
FSEvents/CoreServices. A `launchdJobState` watch's first poll only
establishes a baseline and never fires on its own — the same "don't notify
about whatever state you found the world in" rule the menu bar app's own
compliance-transition notifications follow.

**Sync model:** `syncEventWatches` runs once per report cycle (same call
site as Custom Device Checks), polling `GET
/api/device-data/event-watches?platform=macos` and diffing the result
against whichever watchers are currently running — unchanged watches are
left alone, changed ones are stopped and restarted, new ones are started,
and anything no longer returned is stopped. The watcher goroutines
themselves then run independently of the report ticker until the next sync.
A workspace-wide kill switch (Settings → Event-Driven Detection → Rollout
controls) stops every watch at the next poll regardless of individual watch
settings.

**Remote report-interval override:** the same poll response can carry a
SOAR-pushed `remoteIntervalSec`, letting an admin safely relax this device's
own `interval_sec` (e.g. from 1h to 4h) once event-driven watches are
confirmed catching what matters — the agent's report ticker hot-resets to
match on the very next cycle, no restart needed (same mechanism the Windows
agent uses).

---

## Webhook Endpoints & Payload Structure

* **Reports:** `POST <base_url>/api/device-data/report` and, when
  `report_apps=true`, `POST <base_url>/api/device-data/report-apps`.
* **Custom check definitions poll:** `GET <base_url>/api/device-data/custom-checks?platform=macos`.
* **Headers on every request:**
  * `Content-Type: application/json` (report calls only)
  * `X-Workspace-Slug: <workspace_slug>`
  * `X-Device-Report-Secret: <report_secret>` — omitted once this device has
    a valid mTLS client certificate loaded (see [mTLS Agent
    Authentication](#mtls-agent-authentication) above); the certificate
    presented during the TLS handshake authenticates the request instead.

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
    {
      "identifier": "com.google.Chrome",
      "name": "Google Chrome",
      "version": "125.0.6422.113",
      "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b85"
    }
  ]
}
```

`sha256` is optional — the lowercase-hex SHA256 of the bundle's main executable (`Contents/MacOS/<CFBundleExecutable>`), when `CFBundleExecutable` was present in `Info.plist` and the file was readable. Cached locally (`apphashes.json`, next to `status.json` — see [ARCHITECTURE.md](ARCHITECTURE.md)) so a binary isn't re-hashed every cycle unless it changed. Feeds SOAR's Binary Integrity feature (`backend/docs/settings.md#binary-integrity`): a VirusTotal file-reputation lookup to flag sideloaded/tampered binaries, independent of CVE-based vulnerability matching.

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
   Applivery SOAR Agent to `/Library/Preferences/es.mi-labs.soar.agent.json`
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
  "report_filevault": true,
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

* **Logs:** launchd redirects stdout/stderr straight to disk per
  `es.mi-labs.soar.agent.plist` — no extra setup needed:

  ```bash
  tail -f /var/log/applivery-soar-agent.log
  tail -f /var/log/applivery-soar-agent.err
  ```

  Every cycle logs the resolved Managed Configuration (`Config loaded:
  BaseURL=... RegisterURL=... WorkspaceSlug=... ReportSecret=(set, N
  chars)... BootstrapToken=(set, N chars)...` — neither secret's actual
  value is ever logged) so you can immediately tell whether
  `/Library/Preferences/es.mi-labs.soar.agent.json` was actually read, plus
  the serial number it's reporting under and the HTTP result of each report
  attempt. `mTLS: ...` log lines track registration/renewal separately —
  see [mTLS Agent Authentication](#mtls-agent-authentication) above.
* **Connectivity:** confirm outbound HTTPS to your SOAR instance's `base_url`
  is permitted through any local firewall or proxy.
* **"No WorkspaceSlug plus ReportSecret/BootstrapToken" in the logs:** the
  managed preference file hasn't been deployed yet, or is missing
  `workspace_slug` plus both `report_secret` and `bootstrap_token` — see
  *Configuration Reference* above. Confirm what's actually on disk with
  `cat /Library/Preferences/es.mi-labs.soar.agent.json`.
* **Config was just deployed but the log still shows the old values:** as of
  this build, Managed Configuration is re-read from disk on every report
  cycle (default hourly) — no restart needed, it'll pick it up on the next
  tick. Older builds cached the config once at launch; if you're
  troubleshooting a device that's been running since before this change
  shipped, `sudo launchctl kickstart -k system/es.mi-labs.soar.agent` to
  force an immediate reload rather than waiting out the interval.
* **Device shows "No security attestation reported" in SOAR despite the
  agent's own logs showing a successful POST:** the backend matches reports
  to a device by exact, case-sensitive serial number. Compare the serial
  number this agent logs on each report against the serial Applivery shows
  for that device in its own inventory (`system_profiler
  SPHardwareDataType | grep "Serial"`) — any difference in case, spacing, or
  formatting will silently prevent the match.
