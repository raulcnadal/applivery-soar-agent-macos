import Foundation

/// The macOS mirror of the Windows agent's tray/service IPC directory
/// (%ProgramData%\Applivery\SOAR\) — see agentstatus_macos.go (repo root)
/// for the Go daemon's side of this contract in full, including exactly why
/// the shared directory ends up chmod 1777 (world-writable + sticky) after
/// every daemon write: that's what lets THIS app — running unprivileged,
/// per-console-user, via a LaunchAgent — create the two trigger-*.flag files
/// below, while the sticky bit still stops it (or any other local process)
/// from deleting or overwriting the root-owned, 0644 status.json out from
/// under the daemon. This app never creates the directory itself and never
/// needs to — the daemon owns that (and if the daemon has never run yet,
/// there's nothing for this app to trigger or read anyway).
enum IPCPaths {
    static let dir = "/Library/Application Support/Applivery/SOAR"
    static let statusCachePath = dir + "/status.json"
    static let triggerReportPath = dir + "/trigger-report.flag"
    static let triggerEvaluatePath = dir + "/trigger-evaluate.flag"

    /// Mirrors the Go daemon's WriteTrigger equivalent (consumeTrigger's doc
    /// comment in agentstatus_macos.go): content is never read back by the
    /// daemon, only the file's existence matters, so an RFC3339 timestamp is
    /// written purely so `cat trigger-report.flag` means something to a human
    /// mid-troubleshoot. Best-effort — if this fails (directory not yet
    /// created because the daemon has never completed a write, permissions
    /// not yet loosened, disk full) the button simply does nothing
    /// observable; there is no persistent "pending trigger" state on this
    /// side to roll back.
    static func writeTrigger(at path: String) {
        let timestamp = ISO8601DateFormatter().string(from: Date())
        try? timestamp.write(toFile: path, atomically: true, encoding: .utf8)
    }
}
