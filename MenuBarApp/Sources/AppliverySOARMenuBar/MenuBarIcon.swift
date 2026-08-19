import AppKit

/// Pre-baked light/dark menu-bar glyphs (Resources/Images/menubar-app-*.png)
/// — the dedicated Applivery mark that replaces the placeholder SF Symbol
/// (checkmark.shield) AppDelegate used before a custom design existed.
///
/// Unlike a template image (isTemplate = true, a single asset AppKit
/// auto-inverts for the current menu bar), these are two distinct
/// pre-rendered designs, so AppDelegate.setUpStatusItem picks the matching
/// one explicitly and re-picks it whenever the system appearance changes
/// (NSApp.effectiveAppearance KVO observation there) instead of relying on
/// automatic inversion.
enum MenuBarIcon {
    static let light: NSImage? = load("menubar-app-light")
    static let dark: NSImage? = load("menubar-app-dark")

    /// The menu bar's own icon height convention — matches the size the SF
    /// Symbol placeholder rendered at by default.
    private static let renderSize = NSSize(width: 18, height: 18)

    static func image(for appearance: NSAppearance) -> NSImage? {
        let isDark = appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
        let picked = isDark ? dark : light
        return picked ?? NSImage(systemSymbolName: "checkmark.shield", accessibilityDescription: "Applivery SOAR")
    }

    private static func load(_ name: String) -> NSImage? {
        guard let url = Bundle.module.url(forResource: name, withExtension: "png", subdirectory: "Images") else {
            NSLog("Applivery SOAR: could not locate bundled menu-bar icon \"\(name)\" — falling back to system glyph.")
            return nil
        }
        let image = NSImage(contentsOf: url)
        image?.size = renderSize
        image?.isTemplate = false
        return image
    }
}
