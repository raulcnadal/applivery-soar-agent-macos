import Foundation
import UserNotifications

/// Thin wrapper around UNUserNotificationCenter — the macOS equivalent of
/// the Windows tray's Shell_NotifyIconW/NIF_INFO balloon (showBalloon,
/// tray/main.go). UNUserNotificationCenter requires the calling process to
/// be a properly bundled app (a CFBundleIdentifier under Contents/Info.plist,
/// not a bare command-line binary) to register at all — see this repo's
/// build-pkg.yml for how the Swift executable gets wrapped into
/// "Applivery SOAR.app" before it's ever run.
final class NotificationManager {
    static let shared = NotificationManager()
    private init() {}

    /// Requests permission once at launch. If the user declines, every
    /// subsequent postComplianceAlert call below just silently no-ops
    /// (UNUserNotificationCenter's own behavior) — there's no in-app
    /// fallback banner, matching the Windows tray's own lack of one when a
    /// user has balloon notifications disabled in Windows' own notification
    /// settings.
    func requestAuthorization() {
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound]) { _, error in
            if let error {
                NSLog("Applivery SOAR: notification authorization request failed: \(error.localizedDescription)")
            }
        }
    }

    /// Despite the name (kept for call-site continuity with the original
    /// compliance-transition use), this is a general-purpose "post a
    /// notification now" call — StatusCardView's Force report/Force
    /// evaluate compliance buttons also use it, with the exact same
    /// title/body strings the Windows tray's triggerForceReport/
    /// triggerForceEvaluate pass to showBalloon, so a mixed Windows/macOS
    /// fleet sees identically-worded alerts everywhere this fires. Delivered
    /// immediately (trigger: nil) since every caller represents something
    /// that just happened, never something scheduled for later.
    func postComplianceAlert(title: String, body: String) {
        let content = UNMutableNotificationContent()
        content.title = title
        content.body = body
        content.sound = .default

        let request = UNNotificationRequest(identifier: UUID().uuidString, content: content, trigger: nil)
        UNUserNotificationCenter.current().add(request) { error in
            if let error {
                NSLog("Applivery SOAR: failed to post compliance notification: \(error.localizedDescription)")
            }
        }
    }
}
