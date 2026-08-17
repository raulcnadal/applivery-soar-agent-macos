import AppKit

/// A borderless, non-activating panel used INSTEAD of NSPopover to host the
/// status card.
///
/// Why not NSPopover: Apple's own popover API was never really designed for
/// anchoring to an NSStatusItem (widely documented — AppKit computes its
/// arrow/edge position via internal heuristics that have known, hard-to-
/// predict gap bugs for LSUIElement/accessory apps). This repo tried two
/// targeted fixes on top of NSPopover across two rounds of real-device
/// testing — activating the app before vs. after show(), and flipping
/// preferredEdge between .minY/.maxY — and neither closed a persistent
/// vertical gap between the menu bar icon and the popover's top edge
/// (reported via a side-by-side screenshot comparison against a reference
/// app with zero gap). A custom panel sidesteps the whole class of bug: WE
/// compute the exact screen rect ourselves (see AppDelegate.openPanel), not
/// NSPopover's internal anchor math — this is the standard technique
/// essentially every polished menu-bar app (almost certainly including that
/// reference app) uses for exactly this reason.
///
/// `.nonactivatingPanel` + overriding canBecomeKey below is the standard
/// combination for this: the panel can still become key (so SwiftUI
/// Buttons inside it get normal click/hover/highlight behavior) WITHOUT
/// activating this app or stealing focus from whatever app was frontmost —
/// the same etiquette every other menu-bar-extra observes, and one this app
/// no longer needs NSApp.activate(ignoringOtherApps:) to work around, since
/// positioning is no longer tied to activation state at all.
final class StatusPanel: NSPanel {
    init(contentView: NSView) {
        super.init(
            contentRect: .zero,
            styleMask: [.borderless, .nonactivatingPanel, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        isOpaque = false
        backgroundColor = .clear
        hasShadow = true
        isMovable = false
        hidesOnDeactivate = false
        // .popUpMenu keeps this above normal windows (including another
        // app's full-screen window, matching the old LSUIElement/full-screen
        // support note) without going as far as .screenSaver/.statusBar.
        level = .popUpMenu
        collectionBehavior = [.canJoinAllSpaces, .stationary, .fullScreenAuxiliary]

        // NSVisualEffectView is what gives this the same translucent/
        // vibrant background NSPopover provided for free — .popover is the
        // material Apple's own popovers use, so switching away from
        // NSPopover doesn't lose that look (the user explicitly called this
        // out as something to keep).
        let effect = NSVisualEffectView()
        effect.material = .popover
        effect.blendingMode = .behindWindow
        effect.state = .active
        effect.wantsLayer = true
        effect.layer?.cornerRadius = 12
        effect.layer?.masksToBounds = true

        contentView.translatesAutoresizingMaskIntoConstraints = false
        effect.addSubview(contentView)
        NSLayoutConstraint.activate([
            contentView.leadingAnchor.constraint(equalTo: effect.leadingAnchor),
            contentView.trailingAnchor.constraint(equalTo: effect.trailingAnchor),
            contentView.topAnchor.constraint(equalTo: effect.topAnchor),
            contentView.bottomAnchor.constraint(equalTo: effect.bottomAnchor),
        ])

        self.contentView = effect
    }

    override var canBecomeKey: Bool { true }
    override var canBecomeMain: Bool { false }
}
