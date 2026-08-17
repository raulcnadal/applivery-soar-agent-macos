// swift-tools-version:5.9
import PackageDescription

// This is the SwiftUI menu bar app (Phase 3 of
// backend/docs/macos-agent-parity-roadmap.md) — a separate Swift Package
// from the Go daemon at the repo root, built and wrapped into a proper
// "Applivery SOAR.app" bundle by .github/workflows/build-pkg.yml. A plain
// SPM executable target (rather than a hand-authored .xcodeproj/pbxproj) was
// chosen deliberately: an .xcodeproj is a fragile, largely-opaque binary-ish
// format to author and review without Xcode itself actually open, whereas
// this Package.swift plus plain Swift files are as reviewable/diffable as
// any other source in this repo, and `swift build` on the macos-latest CI
// runner (which already has a full Xcode toolchain) verifies it compiles
// exactly the same way `go build` verifies the daemon — see the roadmap's
// §0 "CI-verified, like the Go agents" decision.
let package = Package(
    name: "AppliverySOARMenuBar",
    platforms: [
        // .v12 (Monterey, 2021) rather than requiring the newer SwiftUI
        // MenuBarExtra API (macOS 13+): this app builds its own NSStatusItem
        // + NSPopover(NSHostingController) instead (see AppDelegate.swift),
        // which works back to far older releases — .v12 is simply a
        // conservative floor for the Swift language/concurrency features
        // used elsewhere in this target, not a hard requirement from the
        // status-bar mechanism itself.
        .macOS(.v12)
    ],
    targets: [
        .executableTarget(
            name: "AppliverySOARMenuBar",
            resources: [
                // Bundled and registered at launch via
                // CTFontManagerRegisterFontsForURL (see FontLoader.swift) —
                // same 3 Outfit weights the Windows tray card embeds, so the
                // status card reads identically to its Windows counterpart
                // instead of silently falling back to San Francisco on a
                // machine that's never had Outfit installed system-wide.
                .copy("Resources/Fonts"),
                // Header banner logo (see BannerImage.swift) — rasterized
                // from assets/applivery-bp-login.svg via cairosvg + an
                // ImageMagick -trim autocrop (removes the SVG's own ~10%
                // whitespace margin, same fix the Windows tray card's own
                // banner_*.bmp assets needed — see tray/card.go's
                // bannerRasterW/H doc comment for the identical rationale),
                // at 6x the source viewBox for a crisp render even at
                // extreme Retina scale factors when displayed small.
                .copy("Resources/Images")
            ]
        )
    ]
)
