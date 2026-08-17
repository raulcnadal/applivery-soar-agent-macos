import AppKit

/// Computes the popover's ideal content width from whatever status data is
/// currently cached — the SwiftUI/NSPopover analog of the Windows tray
/// card's own dynamic cardWidthPx (tray/card.go's buildCardContent):
/// starts from a fixed floor, grows to fit the widest thing it actually has
/// to show (the "Force evaluate compliance" button label, a long device
/// name, or a long Policy name), and is capped at a fraction of the screen
/// width so it can never render partly off-screen. Reported bug this fixes:
/// a fixed 320pt-wide card truncated "Force evaluate compliance" to "Force
/// evaluate compli…" and would have done the same to any Policy name longer
/// than a few words (e.g. "Esquema Nacional de Seguridad (ENS, RD
/// 311/2022) — Windows baseline", visible on the Windows card at that exact
/// width today).
enum CardSizing {
    static let minWidth: CGFloat = 360
    private static let maxWidthFraction: CGFloat = 0.9
    private static let outerPadding: CGFloat = 32 // .padding(16) on both left/right edges
    private static let interButtonSpacing: CGFloat = 8
    private static let buttonChrome: CGFloat = 28 // approx. horizontal chrome .buttonStyle(.bordered) adds around a label on macOS
    private static let pillHorizontalPadding: CGFloat = 16 // 8pt each side, see StatusCardView.pill(text:color:)
    private static let rowGap: CGFloat = 16 // Spacer-driven minimum gap between a row's label and its trailing pill

    static func idealWidth(for cache: StatusCache?) -> CGFloat {
        let regular12 = font("Outfit Regular", size: 12)
        let semibold10 = font("Outfit SemiBold", size: 10)
        let semibold12 = font("Outfit SemiBold", size: 12)
        let semibold15 = font("Outfit SemiBold", size: 15)

        var needed = minWidth

        // The action buttons' own labels are fixed strings, present in
        // every state (even the empty/waiting-for-first-report one) — the
        // card should never be narrower than what they need regardless of
        // whatever device/policy data is or isn't loaded yet.
        let reportW = measure("Force report", font: semibold12)
        let evaluateW = measure("Force evaluate compliance", font: semibold12)
        needed = max(needed, reportW + evaluateW + buttonChrome * 2 + interButtonSpacing + outerPadding)

        guard let cache else { return min(needed, maxWidth()) }

        let deviceName = cache.deviceName?.isEmpty == false ? cache.deviceName! : "This device"
        let widestStatusPill = measure("Unavailable", font: semibold10) + pillHorizontalPadding
        needed = max(needed, measure(deviceName, font: semibold15) + widestStatusPill + rowGap + outerPadding)

        let widestPolicyPill = measure("Violation", font: semibold10) + pillHorizontalPadding
        for policy in cache.compliance.policies {
            needed = max(needed, measure(policy.name, font: regular12) + widestPolicyPill + rowGap + outerPadding)
        }

        return min(needed, maxWidth())
    }

    private static func maxWidth() -> CGFloat {
        (NSScreen.main?.visibleFrame.width ?? 1440) * maxWidthFraction
    }

    private static func font(_ name: String, size: CGFloat) -> NSFont {
        NSFont(name: name, size: size) ?? .systemFont(ofSize: size)
    }

    private static func measure(_ text: String, font: NSFont) -> CGFloat {
        (text as NSString).size(withAttributes: [.font: font]).width
    }
}
