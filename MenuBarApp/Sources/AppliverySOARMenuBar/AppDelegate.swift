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
        // Starting size only — togglePopover recomputes contentSize.width
        // via CardSizing right before every show() (mirroring the Windows
        // tray card's own cardWidthPx, recomputed fresh each time
        // showCard() runs, tray/card.go). Height stays fixed: the card caps
        // visible policy rows at maxPolicyRows (StatusCardView) with a "+N
        // more" summary instead of growing unbounded, so a taller value
        // here isn't needed the way a wider one is.
        popover.contentSize = NSSize(width: CardSizing.minWidth, height: 460)
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

            // store.refresh() is a synchronous local-file read (StatusStore
            // .refresh -> StatusCacheStore.read()), so store.cache already
            // reflects the freshest data by the time this runs — recompute
            // the card's ideal width against it every time the popover
            // opens, same "recompute fresh on every show" behavior as the
            // Windows tray card's own cardWidthPx (tray/card.go).
            popover.contentSize = NSSize(width: CardSizing.idealWidth(for: store.cache), height: popover.contentSize.height)

            // FIX: Anchor and present the popover FIRST against button.bounds.
            // Calling NSApp.activate BEFORE popover.show forces AppKit to evaluate
            // window bounds while the application state is mid-activation, causing
            // the popover arrow to misalign vertically and drift across the screen.
            popover.show(relativeTo: button.bounds, of: button, preferredEdge: .maxY)

            // Make the popover window key and activate the process AFTER anchoring
            popover.contentViewController?.view.window?.makeKey()
            NSApp.activate(ignoringOtherApps: true)
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