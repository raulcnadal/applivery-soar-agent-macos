import Foundation

// Mirrors the Go daemon's status.json contract field-for-field —
// agentstatus_macos.go (repo root) is the source of truth for these shapes;
// if a field is renamed or retyped there, make the matching change here.
// Optional (`?`) properties correspond exactly to the Go side's `*T`
// (nullable pointer) or `omitempty` fields — anything the daemon might
// legitimately omit (e.g. FileVaultStatus before the first successful
// report, or a whole Violations array when there aren't any) decodes to
// `nil` rather than failing the whole document. JSONDecoder is otherwise
// tolerant of unknown/extra keys, so this file doesn't need to change every
// time a purely-additive field lands on the Go side.

struct AgentStatusPolicy: Codable, Identifiable {
    let id: String
    let name: String
    let severity: String
}

struct AgentStatusViolation: Codable {
    let policyId: String
    let policyName: String?
    let severity: String?
    let lastDetectedAt: String?
}

struct AgentStatusCompliance: Codable {
    var available: Bool = false
    var reason: String?
    // Optional, NOT `Bool = false`, even though the Go side's zero value is
    // also false -- this file's own doc comment says Optional here mirrors
    // the Go side's `omitempty`, and Compliant bool (tagged
    // json:"compliant,omitempty" in agentstatus_macos.go) IS omitempty. A
    // non-optional property in a Swift Codable type is decoded via
    // decode(_:forKey:), not
    // decodeIfPresent -- it does NOT fall back to its declared default value
    // when the key is simply absent, it throws DecodingError.keyNotFound,
    // which fails the ENTIRE decode (StatusCacheStore.read() catches that
    // with `try?` and returns nil). Since omitempty omits `compliant` for
    // its own zero value (false), that was every non-compliant device's
    // status.json -- decoding always failed for exactly the devices an
    // admin would most want to see, silently falling back to the "Waiting
    // for the first report" empty state instead of the real violation data
    // (confirmed live: status.json on disk was fully correct and fresh,
    // menu bar still showed the empty state even after a full reboot).
    var compliant: Bool?
    var riskScore: Int?
    var riskTier: String?
    var policies: [AgentStatusPolicy] = []
    var violations: [AgentStatusViolation]?
}

struct StatusCache: Codable {
    var updatedAt: String?
    var workspaceSlug: String?
    var baseUrl: String?
    var serialNumber: String?
    var lastReportAt: String?
    var lastReportOk: Bool = false
    var reportedFileVault: Bool = false
    var fileVaultStatus: Bool?
    var reportedFirewall: Bool = false
    var firewallEnabled: Bool?
    var reportedApps: Bool = false
    var osBuild: String?
    var deviceMatched: Bool = false
    var deviceName: String?
    var compliance: AgentStatusCompliance = AgentStatusCompliance()
}

/// Read/write side of the shared IPC contract with the Go daemon. The daemon
/// owns writing status.json and consuming trigger files (agentstatus_macos.go,
/// status_macos.go, telemetry_macos.go); this app only ever reads the former
/// and creates the latter — see IPCPaths.swift's doc comment for exactly why
/// the shared directory's permissions make that split safe despite this app
/// running unprivileged as the console user while the daemon runs as root.
enum StatusCacheStore {
    /// Returns nil on any failure (file missing, malformed JSON) rather than
    /// throwing — every call site treats "no data yet" as a normal, common
    /// state (this app can easily be running before the daemon's first
    /// report cycle completes), not an error to surface to the user.
    static func read() -> StatusCache? {
        guard let data = try? Data(contentsOf: URL(fileURLWithPath: IPCPaths.statusCachePath)) else {
            return nil
        }
        return try? JSONDecoder().decode(StatusCache.self, from: data)
    }
}
