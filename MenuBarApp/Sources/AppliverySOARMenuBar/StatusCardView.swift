import SwiftUI

/// Direct SwiftUI port of the Windows tray's status card (tray/card.go's
/// buildCardContent) — same sections, same data source (status.json via
/// StatusCache, identical field names), same "waiting for first report"
/// empty state, same cardMaxPolicyRows cap. Layout is native SwiftUI/
/// VStack-HStack rather than card.go's manual GDI pixel-positioning, so
/// exact spacing won't be pixel-identical, but every piece of information
/// shown and every color/threshold is intentionally the same.
struct StatusCardView: View {
    @EnvironmentObject var store: StatusStore
    @State private var forceReportTapped = false
    @State private var forceEvaluateTapped = false

    private let maxPolicyRows = 6

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            if let cache = store.cache {
                header(cache)
                actionButtons()
                Divider().padding(.vertical, 10)
                reportingSection(cache)
                Divider().padding(.vertical, 10)
                complianceSection(cache)
                Divider().padding(.vertical, 10)
                footer(cache)
            } else {
                emptyState()
            }
        }
        .padding(16)
        .frame(width: 320)
        .onAppear {
            FontLoader.registerBundledFonts()
            store.refresh()
        }
    }

    // MARK: - Header

    @ViewBuilder
    private func header(_ cache: StatusCache) -> some View {
        HStack(alignment: .top) {
            Text(cache.deviceName?.isEmpty == false ? cache.deviceName! : "This device")
                .font(.outfit(15, weight: .semibold))
                .foregroundColor(AppColor.textPrimary)
                .lineLimit(1)
            Spacer()
            statusPill(cache)
        }
        .padding(.bottom, 12)
    }

    @ViewBuilder
    private func statusPill(_ cache: StatusCache) -> some View {
        let compliance = cache.compliance
        if !compliance.available {
            pill(text: "Unavailable", color: AppColor.gray400)
        } else if compliance.compliant {
            pill(text: "Compliant", color: AppColor.success)
        } else {
            let count = compliance.violations?.count ?? 0
            pill(text: "\(count) issue\(count == 1 ? "" : "s")", color: AppColor.danger)
        }
    }

    // MARK: - Action buttons

    @ViewBuilder
    private func actionButtons() -> some View {
        HStack(spacing: 8) {
            Button(action: {
                IPCPaths.writeTrigger(at: IPCPaths.triggerReportPath)
                forceReportTapped = true
                DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { forceReportTapped = false }
            }) {
                Text(forceReportTapped ? "Requested" : "Force report")
                    .font(.outfit(12, weight: .medium))
                    .frame(maxWidth: .infinity)
            }
            .buttonStyle(.bordered)
            .disabled(forceReportTapped)

            Button(action: {
                IPCPaths.writeTrigger(at: IPCPaths.triggerEvaluatePath)
                forceEvaluateTapped = true
                DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { forceEvaluateTapped = false }
            }) {
                Text(forceEvaluateTapped ? "Requested" : "Force evaluate compliance")
                    .font(.outfit(12, weight: .medium))
                    .frame(maxWidth: .infinity)
            }
            .buttonStyle(.bordered)
            .disabled(forceEvaluateTapped)
        }
        .padding(.bottom, 4)
    }

    // MARK: - Reporting section

    @ViewBuilder
    private func reportingSection(_ cache: StatusCache) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            sectionTitle("Reporting")

            reportRow(label: "FileVault", gate: cache.reportedFileVault, value: cache.fileVaultStatus)
            reportRow(label: "Firewall", gate: cache.reportedFirewall, value: cache.firewallEnabled)

            if cache.reportedApps {
                HStack {
                    Text("App inventory")
                        .font(.outfit(12))
                        .foregroundColor(AppColor.textPrimary)
                    Spacer()
                    Text("Reported")
                        .font(.outfit(12))
                        .foregroundColor(AppColor.textMuted)
                }
            }

            HStack {
                Text("Last report")
                    .font(.outfit(12))
                    .foregroundColor(AppColor.textPrimary)
                Spacer()
                Text(formatRelativeTime(cache.lastReportAt))
                    .font(.outfit(11))
                    .foregroundColor(AppColor.textMuted)
                pill(text: cache.lastReportOk ? "OK" : "Failed", color: cache.lastReportOk ? AppColor.success : AppColor.danger)
            }
        }
    }

    @ViewBuilder
    private func reportRow(label: String, gate: Bool, value: Bool?) -> some View {
        if gate {
            HStack {
                Text(label)
                    .font(.outfit(12))
                    .foregroundColor(AppColor.textPrimary)
                Spacer()
                switch value {
                case .some(true):
                    pill(text: "Enabled", color: AppColor.success)
                case .some(false):
                    pill(text: "Disabled", color: AppColor.danger)
                case .none:
                    pill(text: "Unknown", color: AppColor.gray400)
                }
            }
        }
    }

    // MARK: - Compliance section

    @ViewBuilder
    private func complianceSection(_ cache: StatusCache) -> some View {
        let compliance = cache.compliance
        VStack(alignment: .leading, spacing: 8) {
            sectionTitle("Compliance")

            if !compliance.available {
                Text(compliance.reason?.isEmpty == false ? compliance.reason! : "Compliance status is not available for this device yet.")
                    .font(.outfit(12))
                    .foregroundColor(AppColor.textMuted)
            } else {
                if let score = compliance.riskScore {
                    riskBar(score: score, tier: compliance.riskTier)
                }

                let violatedIDs = Set((compliance.violations ?? []).map { $0.policyId })
                let policies = compliance.policies
                Text("Policies applied (\(policies.count))")
                    .font(.outfit(11, weight: .medium))
                    .foregroundColor(AppColor.textMuted)

                ForEach(policies.prefix(maxPolicyRows)) { policy in
                    HStack {
                        Text(policy.name)
                            .font(.outfit(12))
                            .foregroundColor(AppColor.textPrimary)
                            .lineLimit(1)
                        Spacer()
                        if violatedIDs.contains(policy.id) {
                            pill(text: "Violation", color: AppColor.danger)
                        } else {
                            pill(text: "OK", color: AppColor.success)
                        }
                    }
                }
                if policies.count > maxPolicyRows {
                    Text("+\(policies.count - maxPolicyRows) more")
                        .font(.outfit(11))
                        .foregroundColor(AppColor.textMuted)
                }
            }
        }
    }

    @ViewBuilder
    private func riskBar(score: Int, tier: String?) -> some View {
        let clamped = max(0, min(100, score))
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text("Risk score")
                    .font(.outfit(12))
                    .foregroundColor(AppColor.textPrimary)
                Spacer()
                Text("\(clamped) · \(tier?.capitalized ?? "Unknown")")
                    .font(.outfit(11, weight: .medium))
                    .foregroundColor(tierColor(tier))
            }
            GeometryReader { geo in
                ZStack(alignment: .leading) {
                    RoundedRectangle(cornerRadius: 3)
                        .fill(AppColor.gray400.opacity(0.25))
                    RoundedRectangle(cornerRadius: 3)
                        .fill(tierColor(tier))
                        .frame(width: geo.size.width * CGFloat(clamped) / 100)
                }
            }
            .frame(height: 6)
        }
    }

    // MARK: - Footer / empty state / shared bits

    @ViewBuilder
    private func footer(_ cache: StatusCache) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            if let slug = cache.workspaceSlug, !slug.isEmpty {
                Text("Managed by \(slug)")
                    .font(.outfit(10))
                    .foregroundColor(AppColor.textMuted)
            }
            Text("Updated \(formatRelativeTime(cache.updatedAt))")
                .font(.outfit(10))
                .foregroundColor(AppColor.textMuted)
        }
    }

    @ViewBuilder
    private func emptyState() -> some View {
        VStack(spacing: 8) {
            Text("Applivery SOAR")
                .font(.outfit(15, weight: .semibold))
                .foregroundColor(AppColor.textPrimary)
            Text("Waiting for the first report from this device")
                .font(.outfit(12))
                .foregroundColor(AppColor.textMuted)
                .multilineTextAlignment(.center)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 24)
    }

    private func sectionTitle(_ text: String) -> some View {
        Text(text.uppercased())
            .font(.outfit(10, weight: .semibold))
            .foregroundColor(AppColor.textMuted)
            .tracking(0.5)
    }

    private func pill(text: String, color: Color) -> some View {
        Text(text)
            .font(.outfit(10, weight: .semibold))
            .foregroundColor(.white)
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .background(Capsule().fill(color))
    }
}
