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
    // Shared with AppDelegate.openPanel, which needs these same numbers to
    // size the window (body height + arrowHeight) and to inset the hosted
    // SwiftUI content below the arrow strip.
    static let cornerRadius: CGFloat = 12
    static let arrowWidth: CGFloat = 16
    static let arrowHeight: CGFloat = 8

    private let effect = NSVisualEffectView()

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
        effect.material = .popover
        effect.blendingMode = .behindWindow
        effect.state = .active
        effect.wantsLayer = true
        // The rounded-rect-plus-triangle mask (updateArrowMask below)
        // replaces a plain cornerRadius+masksToBounds here — that only
        // gave a rectangle with rounded corners, no way to poke a triangle
        // notch out through the top edge for the arrow.

        contentView.translatesAutoresizingMaskIntoConstraints = false
        effect.addSubview(contentView)
        NSLayoutConstraint.activate([
            contentView.leadingAnchor.constraint(equalTo: effect.leadingAnchor),
            contentView.trailingAnchor.constraint(equalTo: effect.trailingAnchor),
            // Inset from the top by arrowHeight, not pinned flush — that
            // strip is reserved for the arrow notch (drawn into the mask,
            // not part of the card content), so text/buttons don't render
            // underneath or get clipped by it.
            contentView.topAnchor.constraint(equalTo: effect.topAnchor, constant: Self.arrowHeight),
            contentView.bottomAnchor.constraint(equalTo: effect.bottomAnchor),
        ])

        self.contentView = effect
    }

    /// Rebuilds the mask that gives this panel its rounded-card-with-a-
    /// speech-bubble-tail shape: a rounded rect for the card body (bottom
    /// `bodyHeight` points) unioned with a small upward-pointing triangle
    /// sitting on top of it (the remaining `arrowHeight` points), centered
    /// on `arrowCenterX` — the status item icon's actual x position, not
    /// necessarily the panel's own horizontal center, since openPanel
    /// clamps the panel's x origin to stay on-screen near display edges.
    /// Called fresh every time the panel opens (AppDelegate.openPanel),
    /// since width/bodyHeight both vary with the card's current content.
    func updateArrowMask(width: CGFloat, bodyHeight: CGFloat, arrowCenterX: CGFloat) {
        let totalHeight = bodyHeight + Self.arrowHeight
        let path = CGMutablePath()
        path.addRoundedRect(
            in: CGRect(x: 0, y: 0, width: width, height: bodyHeight),
            cornerWidth: Self.cornerRadius,
            cornerHeight: Self.cornerRadius
        )

        let halfArrow = Self.arrowWidth / 2
        // Keep the triangle from sliding into (or past) a rounded corner
        // if the icon sits very close to the panel's clamped edge.
        let centerX = min(max(arrowCenterX, Self.cornerRadius + halfArrow), width - Self.cornerRadius - halfArrow)
        path.move(to: CGPoint(x: centerX - halfArrow, y: bodyHeight))
        path.addLine(to: CGPoint(x: centerX, y: totalHeight))
        path.addLine(to: CGPoint(x: centerX + halfArrow, y: bodyHeight))
        path.closeSubpath()

        let mask = CAShapeLayer()
        mask.path = path
        mask.fillColor = NSColor.black.cgColor
        mask.frame = CGRect(x: 0, y: 0, width: width, height: totalHeight)
        effect.layer?.mask = mask
    }

    override var canBecomeKey: Bool { true }
    override var canBecomeMain: Bool { false }

    /// The logged geometry from a real device (see AppDelegate.openPanel's
    /// diagnostics) shows this panel's intended top edge lands 1pt *above*
    /// screen.visibleFrame's top boundary — i.e. it deliberately overlaps
    /// the reserved menu-bar strip by a hair, which is exactly what "flush
    /// against the icon" requires, since the icon's own bottom edge sits
    /// right at that same boundary. AppKit's default behavior is to
    /// automatically push ("constrain") any window's frame back inside
    /// visibleFrame during makeKeyAndOrderFront, which would silently
    /// override setFrameOrigin's carefully-computed position — plausibly
    /// explaining why a screenshot showed a large gap despite the logged
    /// origin being mathematically flush. Overriding this to a no-op is the
    /// standard, documented way menu-bar-anchored panels opt out of that
    /// safety behavior (Apple's own NSPopover backing window does the same
    /// internally). Should be revisited if this panel ever needs to support
    /// being dragged or resized by the user, since constraining normally
    /// also keeps user-moved windows from disappearing off-screen.
    override func constrainFrameRect(_ frameRect: NSRect, to screen: NSScreen?) -> NSRect {
        frameRect
    }
}
