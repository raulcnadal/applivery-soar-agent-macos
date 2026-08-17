import AppKit
import SwiftUI
import UserNotifications

/// The macOS equivalent of the Windows tray's tray/main.go: owns the
/// NSStatusItem (menu bar icon), the popover that hosts StatusCardView, and
/// kicks off StatusStore's 60s refresh loop. LSUIElement=true in Info.plist
/// (set on the wrapped .app bundle by build-pkg.yml) keeps this out of the
/// Dock and Cmd+Tab switcher, same as the Windows tray having no taskbar
/// window of its own — belt-and-suspenders reinforced here via
/// NSApp.setActivationPolicy(.accessory) in case LSUIElement is ever
/// dropped from a future Info.plist edit.
final class AppDelegate: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate {
    private var statusItem: NSStatusItem?
    private var popover: NSPopover?
    private let store = StatusStore()

    func applicationDidFinishLaunching(_ notification: Foundation.Notification) {
        NSApp.setActivationPolicy(.accessory)

        FontLoader.registerBundledFonts()

        UNUserNotificationCenter.current().delegate = self
        NotificationManager.shared.requestAuthorization()

        setUpStatusItem()
        setUpPopover()

        store.start()
    }

    func applicationWillTerminate(_ notification: Foundation.Notification) {
        store.stop()
    }

    private func setUpStatusItem() {
        let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        // A plain SF Symbol stands in for a dedicated menu-bar glyph for
        // now — isTemplate lets AppKit auto-invert it for light/dark menu
        // bars, same visual behavior the Windows tray gets "for free" from
        // Shell_NotifyIconW's own icon handling. A custom Applivery mark can
        // replace this without touching any other file once one exists.
        let image = NSImage(systemSymbolName: "checkmark.shield", accessibilityDescription: "Applivery SOAR")
        image?.isTemplate = true
        item.button?.image = image
        item.button?.target = self
        item.button?.action = #selector(togglePopover(_:))
        statusItem = item
    }

    private func setUpPopover() {
        let popover = NSPopover()
        popover.behavior = .transient
        popover.contentSize = NSSize(width: 320, height: 420)
        popover.contentViewController = NSHostingController(
            rootView: StatusCardView().environmentObject(store)
        )
        self.popover = popover
    }

    @objc private func togglePopover(_ sender: AnyObject?) {
        guard let button = statusItem?.button, let popover else { return }
        if popover.isShown {
            popover.performClose(sender)
        } else {
            // Opening the popover is this app's equivalent of the Windows
            // tray card's own "always re-read on open" behavior
            // (card.go's buildCardContent calling readStatusCache fresh
            // every time) — never more than one report cycle stale.
            store.refresh()

            // Reported bug (field testing, Aug 2026): the popover rendered
            // far from the actual status item, roughly centered on-screen
            // instead of anchored below the icon. Root cause: this process
            // is LSUIElement/.accessory and launchd-started, so it has never
            // been made the frontmost app — AppKit's screen-coordinate
            // conversion for NSPopover.show(relativeTo:of:) is unreliable
            // for a never-activated accessory app in exactly this way. This
            // is the standard fix: force activation once, immediately before
            // showing, so AppKit has a valid active-app context to position
            // relative to. Cheap/idempotent to call every time (no visible
            // effect once already active).
            NSApp.activate(ignoringOtherApps: true)
            popover.show(relativeTo: button.bounds, of: button, preferredEdge: .minY)
        }
    }

    /// Show the compliance-transition banner even while this app is the
    /// foreground/active app (e.g. the popover is open when a transition
    /// fires) — without this override, UNUserNotificationCenter's default
    /// behavior suppresses banners for the frontmost app, which would mean
    /// a notification silently never appears at the one moment a user is
    /// actually looking at this app.
    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification,
        withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
    ) {
        completionHandler([.banner, .sound])
    }
}
