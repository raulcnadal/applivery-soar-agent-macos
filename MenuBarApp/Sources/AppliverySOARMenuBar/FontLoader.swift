import Foundation
import CoreText
import SwiftUI

/// Registers the 3 bundled Outfit weights (Resources/Fonts/) with the
/// process's font registry via CTFontManagerRegisterFontsForURL, mirroring
/// the Windows tray's own embedded-font approach (tray/fonts/, loaded via
/// AddFontMemResourceEx) — without this, Font.custom("Outfit", ...) below
/// silently falls back to the system font on any Mac that's never had
/// Outfit installed, which is effectively every Mac.
enum FontLoader {
    /// These 3 static instances (produced the same way the Windows tray's own
    /// embedded fonts were — fonttools varLib.instancer against Google
    /// Fonts' variable Outfit, re-tagged via fonttools' name-table API) are
    /// each their own standalone font FAMILY as far as CoreText is
    /// concerned, not 3 weights of one "Outfit" family — confirmed by
    /// reading each file's name table (family/full/PostScript name is
    /// literally "Outfit Regular"/"Outfit SemiBold"/"Outfit Bold", subfamily
    /// "Regular" on all three). So Font.custom below must reference these
    /// exact strings directly; there is no single "Outfit" family to look up
    /// a .weight() modifier against.
    static let regularFamily = "Outfit Regular"
    static let semiboldFamily = "Outfit SemiBold"
    static let boldFamily = "Outfit Bold"

    private static var didRegister = false

    static func registerBundledFonts() {
        guard !didRegister else { return }
        didRegister = true

        let names = ["Outfit-Regular", "Outfit-SemiBold", "Outfit-Bold"]
        for name in names {
            guard let url = Bundle.module.url(forResource: name, withExtension: "ttf", subdirectory: "Fonts") else {
                NSLog("Applivery SOAR: could not locate bundled font \(name).ttf — falling back to the system font.")
                continue
            }
            var errorRef: Unmanaged<CFError>?
            if !CTFontManagerRegisterFontsForURL(url as CFURL, .process, &errorRef) {
                let message = errorRef?.takeRetainedValue().localizedDescription ?? "unknown error"
                NSLog("Applivery SOAR: failed to register font \(name): \(message)")
            }
        }
    }
}

extension Font {
    /// Weight-aware helper so call sites in StatusCardView read the same way
    /// the Windows card's own font-selection helper does (pick a weight,
    /// pick a size) rather than hardcoding "Outfit SemiBold" string literals
    /// throughout the view. Only 3 real weights are embedded — anything
    /// requested other than .semibold/.bold below falls back to Regular,
    /// same 3-weight ceiling the Windows tray card itself has.
    static func outfit(_ size: CGFloat, weight: Font.Weight = .regular) -> Font {
        switch weight {
        case .bold, .heavy, .black:
            return .custom(FontLoader.boldFamily, size: size)
        case .semibold, .medium:
            return .custom(FontLoader.semiboldFamily, size: size)
        default:
            return .custom(FontLoader.regularFamily, size: size)
        }
    }
}
