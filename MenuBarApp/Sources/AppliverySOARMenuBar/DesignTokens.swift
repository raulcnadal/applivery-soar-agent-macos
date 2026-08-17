import SwiftUI

/// Same hex values as the Windows tray card's own design tokens
/// (tray/card.go) — kept identical on purpose so a mixed Windows/macOS
/// fleet's status cards read as the same product, not two skins.
enum AppColor {
    static let brand = Color(hex: 0x0241E3)
    static let success = Color(hex: 0x22C55E)
    static let danger = Color(hex: 0xEF4444)
    static let warning = Color(hex: 0xF59E0B)
    static let critical = Color(hex: 0xB91C1C)
    static let low = Color(hex: 0x64748B)
    static let gray400 = Color(hex: 0x9CA3AF)
    static let textPrimary = Color.primary
    static let textMuted = Color.secondary
}

extension Color {
    init(hex: UInt32, opacity: Double = 1) {
        let r = Double((hex >> 16) & 0xFF) / 255
        let g = Double((hex >> 8) & 0xFF) / 255
        let b = Double(hex & 0xFF) / 255
        self.init(red: r, green: g, blue: b, opacity: opacity)
    }
}

/// Direct port of tray/card.go's tierColor — maps a risk tier string to the
/// same 4-color ramp (critical/high/medium/low), with the same
/// gray400 fallback for an unrecognized or absent tier.
func tierColor(_ tier: String?) -> Color {
    switch tier?.lowercased() {
    case "critical": return AppColor.critical
    case "high": return AppColor.danger
    case "medium": return AppColor.warning
    case "low": return AppColor.low
    default: return AppColor.gray400
    }
}

/// Direct port of tray/main.go's formatRelativeTime bucketing: <1min="just
/// now", <1h="Xm ago", <24h="Xh ago", else "Xd ago". Takes an RFC3339 string
/// (as status.json always encodes timestamps) rather than a Date so call
/// sites don't each need their own ISO8601DateFormatter/parse-failure
/// handling — an unparseable or nil timestamp renders as "—", never a crash
/// or an empty string that reads like a layout bug.
func formatRelativeTime(_ rfc3339: String?) -> String {
    guard let rfc3339, let date = ISO8601DateFormatter().date(from: rfc3339) else {
        return "—"
    }
    let seconds = max(0, Date().timeIntervalSince(date))
    switch seconds {
    case ..<60:
        return "just now"
    case ..<3600:
        return "\(Int(seconds / 60))m ago"
    case ..<86400:
        return "\(Int(seconds / 3600))h ago"
    default:
        return "\(Int(seconds / 86400))d ago"
    }
}
