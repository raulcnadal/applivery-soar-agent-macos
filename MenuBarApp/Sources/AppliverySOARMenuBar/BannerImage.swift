import AppKit

/// Loads the header banner logo (Resources/Images/applivery-banner.png) once
/// and hands back both the NSImage and its true pixel aspect ratio — the
/// direct macOS analog of the Windows tray card's loadBannerBitmap
/// (tray/card.go), which likewise loads a pre-rasterized bitmap rather than
/// rendering the source SVG live (this repo targets macOS 12+; native SVG
/// decoding wasn't reliable across that whole range, so cairosvg + an
/// ImageMagick -trim autocrop did the rasterization once, offline — see
/// Package.swift's resource-copy doc comment for the exact pipeline).
enum BannerImage {
    static let image: NSImage? = {
        guard let url = Bundle.module.url(forResource: "applivery-banner", withExtension: "png", subdirectory: "Images") else {
            NSLog("Applivery SOAR: could not locate bundled banner image — header will render without it.")
            return nil
        }
        return NSImage(contentsOf: url)
    }()

    /// width/height of the source bitmap — StatusCardView uses this to size
    /// the rendered banner at a fixed height while preserving its true
    /// aspect ratio, same relationship the Windows card computes from its
    /// own bannerRasterW/bannerRasterH constants.
    static let aspectRatio: CGFloat = {
        guard let size = image?.size, size.height > 0 else { return 8.24 }
        return size.width / size.height
    }()
}
