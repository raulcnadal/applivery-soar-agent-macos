import AppKit
import SwiftUI
import UserNotifications

/// The macOS equivalent of the Windows tray's tray/main.go: owns the
/// NSStatusItem (menu bar icon), the panel that hosts StatusCardView, and
/// kicks off StatusStore's 60s refresh loop. LSUIElement=true in Info.plist
/// (set on the wrapped .app bundle by build-pkg.yml) keeps this out of the
/// Dock and Cmd+Tab switcher, same as the Windows tray having no taskbar
/// window of its own — belt-and-suspenders reinforced here via
/// NSApp.setActivationPolicy(.accessory) in case LSUIElement is ever
/// dropped from a future Info.plist edit.
///
/// This hosts the card in a custom StatusPanel rather than NSPopover — see
/// StatusPanel.swift's doc comment for why. openPanel below computes the
/// panel's screen frame explicitly from the status item button's own
/// converted screen rect, so its top edge is pinned flush against the
/// button's bottom edge with zero gap, deterministically, instead of
/// relying on NSPopover's internal (here, buggy) anchor math.
final class AppDelegate: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate {
    private var statusItem: NSStatusItem?
    private var panel: StatusPanel?
    private var hostingView: NSHostingView<AnyView>?
    private let store = StatusStore()
    private var outsideClickMonitor: Any?

    // Initial-measurement height only, tall enough that no content clips
    // during the fitting-size pass in openPanel below — not the height the
    // panel actually ends up at (that's measured fresh via
    // hostingView.fittingSize every time the panel opens, since it varies
    // with how many policy rows the current device has).
    private static let panelHeight: CGFloat = 460
    private static let screenEdgeMargin: CGFloat = 8

    func applicationDidFinishLaunching(_ notification: Foundation.Notification) {
        NSApp.setActivationPolicy(.accessory)

        FontLoader.registerBundledFonts()

        UNUserNotificationCenter.current().delegate = self
        NotificationManager.shared.requestAuthorization()

        setUpStatusItem()
        setUpPanel()

        store.start()
    }

    func applicationWillTerminate(_ notification: Foundation.Notification) {
        store.stop()
        stopOutsideClickMonitor()
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
        item.button?.action = #selector(togglePanel(_:))
        statusItem = item
    }

    private func setUpPanel() {
        let hosting = NSHostingView(rootView: AnyView(StatusCardView().environmentObject(store)))
        hostingView = hosting
        panel = StatusPanel(contentView: hosting)
    }

    @objc private func togglePanel(_ sender: AnyObject?) {
        // TEMPORARY diagnostics (see openPanel's own diagnostics block
        // below for the full story): a prior round of this same logging
        // never printed anything at all despite the click demonstrably
        // firing (trackMouse/sendAction showed up in the unified log), on a
        // build hash-verified to contain this exact code. That means
        // control never reached the log line inside openPanel — most
        // likely one of the two guard clauses below is bailing silently.
        // Logging unconditionally at entry, and explicitly on each bail
        // path, so this can't happen again: some line will always print.
        let entryMsg = "Applivery SOAR [panel-diagnostics]: togglePanel fired. statusItem=\(String(describing: statusItem)) button=\(String(describing: statusItem?.button)) panel=\(String(describing: panel))"
        NSLog("%@", entryMsg)
        DebugLog.write(entryMsg)
        guard let button = statusItem?.button else {
            NSLog("%@", "Applivery SOAR [panel-diagnostics]: togglePanel bailing — statusItem.button is nil")
            DebugLog.write("togglePanel bailing — statusItem.button is nil")
            return
        }
        guard let panel else {
            NSLog("%@", "Applivery SOAR [panel-diagnostics]: togglePanel bailing — panel is nil")
            DebugLog.write("togglePanel bailing — panel is nil")
            return
        }
        if panel.isVisible {
            NSLog("%@", "Applivery SOAR [panel-diagnostics]: closing (panel.isVisible was true)")
            DebugLog.write("closing (panel.isVisible was true)")
            closePanel()
        } else {
            NSLog("%@", "Applivery SOAR [panel-diagnostics]: opening (panel.isVisible was false)")
            DebugLog.write("opening (panel.isVisible was false)")
            openPanel(relativeTo: button)
        }
    }

    private func openPanel(relativeTo button: NSStatusBarButton) {
        guard let panel else {
            NSLog("%@", "Applivery SOAR [panel-diagnostics]: openPanel bailing — panel is nil")
            DebugLog.write("openPanel bailing — panel is nil")
            return
        }
        guard let buttonWindow = button.window else {
            NSLog("%@", "Applivery SOAR [panel-diagnostics]: openPanel bailing — button.window is nil")
            DebugLog.write("openPanel bailing — button.window is nil")
            return
        }

        // Opening the panel is this app's equivalent of the Windows tray
        // card's own "always re-read on open" behavior (card.go's
        // buildCardContent calling readStatusCache fresh every time) —
        // never more than one report cycle stale.
        store.refresh()

        // store.refresh() is a synchronous local-file read (StatusStore
        // .refresh -> StatusCacheStore.read()), so store.cache already
        // reflects the freshest data by the time this runs — recompute the
        // card's ideal width against it every time the panel opens, same
        // "recompute fresh on every show" behavior as the Windows tray
        // card's own cardWidthPx (tray/card.go).
        let width = CardSizing.idealWidth(for: store.cache)

        // ROOT CAUSE of the persistent vertical gap, finally nailed down via
        // a post-order frame readback on a real device: this app's content
        // is entirely Auto-Layout-driven (StatusCardView's SwiftUI tree,
        // hosted via NSHostingView, pinned to all 4 edges of the panel's
        // NSVisualEffectView contentView). Once a window's content view is
        // under Auto Layout, AppKit lets the layout system's own resolved
        // size win over whatever setContentSize was told, the first time
        // layout actually runs — which on a real device showed up as
        // panelSize=360x460 requested but actualFrame height=398 (a card
        // with few policy rows naturally wants less height). Because
        // setFrameOrigin only pins the BOTTOM-LEFT corner, that 62pt shrink
        // came entirely off the TOP — exactly the dead space between the
        // icon and the card that every prior positioning fix (activation
        // ordering, preferredEdge, the NSPanel rewrite itself, constraining
        // to visibleFrame) failed to touch, because none of them were wrong
        // about x/y — the HEIGHT used to compute y was wrong.
        //
        // Fix: measure the real height BEFORE computing origin, instead of
        // guessing Self.panelHeight and hoping it sticks. First lock in the
        // width alone (Auto Layout can't resolve a fitting height without
        // knowing width first, since rows wrap/pill-size against it), force
        // a synchronous layout pass, then ask the hosting view directly for
        // its fitting size — the dedicated API for exactly this, rather
        // than inferring height indirectly from window auto-resize
        // behavior (which is what went wrong in the first place).
        panel.setContentSize(NSSize(width: width, height: Self.panelHeight))
        panel.contentView?.layoutSubtreeIfNeeded()
        let height = hostingView?.fittingSize.height ?? Self.panelHeight
        panel.setContentSize(NSSize(width: width, height: height))

        // Convert the button's own bounds to screen coordinates directly —
        // no dependency on app/window activation state, which is what made
        // NSPopover's positioning flaky here across two earlier fix
        // attempts. minY of this rect is the button's BOTTOM edge (AppKit
        // screen coordinates have their origin at the bottom-left, and the
        // menu bar sits at the top of the screen), so pinning the panel's
        // top there — origin.y = minY - height — is what makes it flush
        // against the icon with zero gap, now using the REAL height.
        let buttonScreenFrame = buttonWindow.convertToScreen(button.convert(button.bounds, to: nil))
        var origin = NSPoint(x: buttonScreenFrame.midX - width / 2, y: buttonScreenFrame.minY - height)

        if let screen = buttonWindow.screen ?? NSScreen.main {
            origin.x = min(origin.x, screen.visibleFrame.maxX - width - Self.screenEdgeMargin)
            origin.x = max(origin.x, screen.visibleFrame.minX + Self.screenEdgeMargin)
        }

        // TEMPORARY diagnostics for the still-unresolved vertical-gap bug
        // (github.com/raulcnadal/applivery-soar-agent-macos — this gap
        // survived 3 prior positioning fixes on top of NSPopover and now
        // this NSPanel rewrite too, all with no visible change, which means
        // the bug isn't in the positioning math itself — something upstream
        // (button.window/button.bounds/convertToScreen) is very likely
        // reporting an unexpected value on the affected machine). Logging
        // every input to the computation so the next fix can be based on
        // the real numbers instead of another blind guess. Safe to remove
        // once the gap is confirmed fixed.
        // %@ with the fully-built message as a single argument (rather than
        // handing the message straight to NSLog as its own format string)
        // — if any interpolated value here ever happened to contain a "%"
        // character, NSLog would try to interpret it as a format
        // specifier, which can silently mangle or drop the whole line.
        // Cheap insurance now that a previous round of this exact logging
        // printed nothing at all on a confirmed-correct build.
        let diagnosticsMessage = """
        Applivery SOAR [panel-diagnostics]: \
        button.bounds=\(button.bounds) \
        buttonWindow.frame=\(buttonWindow.frame) \
        buttonScreenFrame=\(buttonScreenFrame) \
        NSStatusBar.system.thickness=\(NSStatusBar.system.thickness) \
        screen.frame=\(String(describing: (buttonWindow.screen ?? NSScreen.main)?.frame)) \
        screen.visibleFrame=\(String(describing: (buttonWindow.screen ?? NSScreen.main)?.visibleFrame)) \
        panelSize=\(width)x\(height) \
        computedOrigin=\(origin)
        """
        NSLog("%@", diagnosticsMessage)
        DebugLog.write(diagnosticsMessage)

        panel.setFrameOrigin(origin)
        panel.makeKeyAndOrderFront(nil)

        // Read back the ACTUAL final frame after ordering front — if
        // AppKit's automatic screen-edge constraining (see StatusPanel's
        // constrainFrameRect override, added this same round) was silently
        // overriding the origin set two lines above, intendedOrigin and
        // actualFrame.origin will differ here. If they match and a gap is
        // still visible on screen, the cause is something else entirely.
        let readbackMsg = "Applivery SOAR [panel-diagnostics]: post-order readback — intendedOrigin=\(origin) actualFrame=\(panel.frame)"
        NSLog("%@", readbackMsg)
        DebugLog.write(readbackMsg)

        startOutsideClickMonitor()
    }

    private func closePanel() {
        panel?.orderOut(nil)
        stopOutsideClickMonitor()
    }

    /// NSPopover's .transient behavior (auto-close on an outside click) has
    /// no equivalent on NSPanel, so it's reimplemented here with a global
    /// event monitor. A *global* monitor only fires for events delivered to
    /// OTHER processes (Apple's own documented behavior), which is exactly
    /// what's wanted: a click on this app's own status item button is
    /// handled directly by togglePanel's own isVisible check (so clicking
    /// the icon again correctly toggles the panel closed instead of the
    /// monitor and the button action racing each other), and a click
    /// anywhere inside this app's own panel (e.g. the Force report button)
    /// never reaches this monitor at all since it belongs to this process —
    /// so it can't be mistaken for an "outside" click either.
    private func startOutsideClickMonitor() {
        stopOutsideClickMonitor()
        outsideClickMonitor = NSEvent.addGlobalMonitorForEvents(matching: [.leftMouseDown, .rightMouseDown]) { [weak self] _ in
            self?.closePanel()
        }
    }

    private func stopOutsideClickMonitor() {
        if let outsideClickMonitor {
            NSEvent.removeMonitor(outsideClickMonitor)
        }
        outsideClickMonitor = nil
    }

    /// Show the compliance-transition banner even while this app is the
    /// foreground/active app (e.g. the panel is open when a transition
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