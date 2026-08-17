import Foundation
import Combine

/// Owns the in-memory copy of status.json the SwiftUI card renders, the 60s
/// refresh timer, and compliance-transition notification logic — the direct
/// analog of the Windows tray's tray/main.go (readStatusCache,
/// refreshIntervalMs's timer callback, checkComplianceTransition).
final class StatusStore: ObservableObject {
    @Published private(set) var cache: StatusCache?

    /// -1 means "not observed yet" — mirrors tray/main.go's
    /// `var lastViolationCount = -1` exactly, including the same
    /// consequence: whatever compliance state this device is already in
    /// when the app starts is only recorded, never notified about. Also
    /// like the Windows tray, this is in-memory only and intentionally NOT
    /// persisted (e.g. to UserDefaults) across restarts — a menu bar app
    /// relaunch (crash, logout/login, LaunchAgent restart) re-baselines
    /// silently rather than potentially re-firing a notification for a
    /// transition that already happened and was already seen.
    private var lastViolationCount = -1

    private var refreshTimer: Timer?

    /// 60s — identical cadence to the Windows tray's own refreshTimerID
    /// (tray/main.go). The status card itself always does its own fresh
    /// read on open (see StatusCardView's .onAppear), independent of this
    /// timer, so opening the card is never more than one report cycle
    /// stale — same behavior as buildCardContent's own readStatusCache call
    /// on the Windows side.
    private let refreshInterval: TimeInterval = 60

    func start() {
        refresh()
        refreshTimer?.invalidate()
        refreshTimer = Timer.scheduledTimer(withTimeInterval: refreshInterval, repeats: true) { [weak self] _ in
            self?.refresh()
        }
    }

    func stop() {
        refreshTimer?.invalidate()
        refreshTimer = nil
    }

    /// Re-reads status.json and checks for a compliance transition. Safe to
    /// call at any time (card open, timer tick, or a manual pull-to-refresh
    /// if one is ever added) — it's idempotent aside from the notification
    /// side effect, which only fires on an actual 0→N / N→0 edge.
    func refresh() {
        let newCache = StatusCacheStore.read()
        checkComplianceTransition(newCache)
        cache = newCache
    }

    /// Direct port of tray/main.go's checkComplianceTransition: fires only
    /// on a strict 0→N (issues just appeared) or N→0 (issues just cleared)
    /// edge, and only when the backend's own compliance evaluation is
    /// available (Available == true) — a device with Available == false
    /// (mTLS not yet issued, no Automation Credential configured, a
    /// transient network error) never fires a false "compliant"/"restored"
    /// notification just because Violations happens to be empty in that
    /// state too.
    private func checkComplianceTransition(_ newCache: StatusCache?) {
        guard let newCache, newCache.compliance.available else { return }
        let count = newCache.compliance.violations?.count ?? 0

        if lastViolationCount == -1 {
            lastViolationCount = count
            return
        }
        if count > 0 && lastViolationCount == 0 {
            let policyWord = count == 1 ? "policy is" : "policies are"
            NotificationManager.shared.postComplianceAlert(
                title: "Compliance issue detected",
                body: "\(count) \(policyWord) now failing on this device."
            )
        } else if count == 0 && lastViolationCount > 0 {
            NotificationManager.shared.postComplianceAlert(
                title: "Compliance restored",
                body: "This device is compliant with all applicable policies again."
            )
        }
        lastViolationCount = count
    }
}
