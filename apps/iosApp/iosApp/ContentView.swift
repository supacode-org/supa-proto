import Shared
import SwiftUI
import TerminalUI

struct ContentView: View {
    @EnvironmentObject private var model: AppModel

    var body: some View {
        NavigationStack {
            HomePage {
                HStack {
                    TerminalPromptTitle(Glyph.command)
                    Spacer()
                    TerminalIconButton(.tune, label: String(localized: "Settings")) {
                        SettingsPresenter.present(model: model)
                    }
                }
                SessionCard(
                    state: model.state,
                    server: model.selectedProfile?.name,
                    routing: model.settings.routingMode,
                    traffic: model.traffic,
                    history: model.trafficHistory,
                    unit: model.settings.trafficUnit
                )
                if case .failed = onEnum(of: model.state) {
                    TerminalBanner(model.state.label, kind: .danger, icon: .hidden)
                }
                connectButton
            } bottom: {
                TerminalCard {
                    NavigationLink {
                        ServerListView()
                    } label: {
                        NavRow(
                            label: String(localized: "Server"),
                            title: model.selectedProfile?.name ?? String(localized: "Add a server"),
                            subtitle: model.selectedProfile?.server,
                            trailing: model.selectedProfile.flatMap { model.pings[$0.server] }.map { ($0.label, $0.grade.color) }
                        )
                    }
                    TerminalCardDivider()
                    Button {
                        SettingsPresenter.present(model: model)
                    } label: {
                        NavRow(label: String(localized: "Routing"), title: model.settings.routingMode.title)
                    }
                    TerminalCardDivider()
                    Button {
                        SettingsPresenter.present(model: model)
                    } label: {
                        NavRow(
                            label: String(localized: "Kill switch"),
                            title: nil,
                            trailing: model.settings.killSwitch
                                ? (String(localized: "On"), TerminalColors.accentText)
                                : (String(localized: "Off"), TerminalColors.textSecondary)
                        )
                    }
                }
                .buttonStyle(.plain)
            } footer: {
                SystemFooter(memory: model.traffic?.memory)
            }
            .toolbar(.hidden, for: .navigationBar)
        }
    }

    private var isDisconnecting: Bool {
        if case .disconnecting = onEnum(of: model.state) { return true }
        return false
    }

    @ViewBuilder private var connectButton: some View {
        if model.state.isActive {
            let label = String(localized: "Disconnect")
            Button(action: model.disconnect) {
                // The small size of the outlined actions, as before the library.
                Text(verbatim: TerminalGlyph.bracketed(label)).font(TerminalType.small)
            }
            .buttonStyle(TerminalButtonStyle(.danger, size: .lg, block: true))
            .disabled(isDisconnecting)
            .accessibilityLabel(Text(label))
        } else {
            let label = String(localized: "Connect")
            Button(action: model.connect) {
                Text(verbatim: TerminalGlyph.prompted(label))
            }
            .buttonStyle(TerminalButtonStyle(.primary, size: .lg, block: true))
            .disabled(model.selectedProfile == nil)
            .accessibilityLabel(Text(label))
        }
    }
}

/// `session.json`: the connection as a small JSON object, then live traffic.
private struct SessionCard: View {
    let state: ConnectionState
    let server: String?
    let routing: RoutingMode
    let traffic: TrafficStats?
    let history: [TrafficPoint]
    let unit: TrafficUnit

    private var isLive: Bool {
        if case .connected = onEnum(of: state) { return true }
        return false
    }

    private var stateValue: (String, Color) {
        switch onEnum(of: state) {
        case .connected: (Glyph.stateConnected, TerminalColors.accentText)
        case .connecting: (Glyph.stateConnecting, TerminalColors.warning)
        case .disconnecting: (Glyph.stateDisconnecting, TerminalColors.warning)
        case .failed: (Glyph.stateFailed, TerminalColors.danger)
        case .disconnected: (Glyph.stateDisconnected, TerminalColors.textSecondary)
        }
    }

    var body: some View {
        TerminalCard {
            HStack {
                Text(verbatim: Glyph.sessionFile)
                Spacer()
                Circle().fill(isLive ? TerminalColors.accent : TerminalColors.textSecondary).frame(width: 7, height: 7)
                Text(verbatim: isLive ? Glyph.live : Glyph.idle)
                    .foregroundStyle(isLive ? TerminalColors.accentText : TerminalColors.textSecondary)
            }
            .font(TerminalType.label)
            .foregroundStyle(TerminalColors.textSecondary)
            .padding(.horizontal, TerminalSpacing.space14)
            .padding(.vertical, TerminalSpacing.space10)
            .accessibilityHidden(true)
            TerminalCardDivider()
            json
                .font(TerminalType.code)
                .lineSpacing(6)
                .padding(.horizontal, TerminalSpacing.space14)
                .padding(.vertical, TerminalSpacing.space12)
                .frame(maxWidth: .infinity, alignment: .leading)
                .accessibilityElement()
                .accessibilityLabel(Text("\(state.label), routing: \(routing.title)"))
            TerminalCardDivider()
            if isLive, let traffic {
                TrafficPanel(traffic: traffic, history: history, unit: unit)
            } else {
                TerminalNote(String(localized: "Live traffic appears once connected")).padding(TerminalSpacing.space14)
            }
        }
    }

    private var json: Text {
        let punctuation = TerminalColors.textSecondary
        func key(_ name: String) -> Text {
            Text(verbatim: "  ") + Text(verbatim: "\"\(name)\"").foregroundColor(TerminalColors.syntaxKey) + Text(verbatim: ": ").foregroundColor(punctuation)
        }
        let (value, color) = stateValue
        let serverValue = server.map { Text(verbatim: "\"\($0)\"").foregroundColor(TerminalColors.syntaxString) }
            ?? Text(verbatim: Glyph.null).foregroundColor(TerminalColors.syntaxNumber)
        return Text(verbatim: "{\n").foregroundColor(punctuation)
            + key(Glyph.keyState) + Text(verbatim: "\"\(value)\"").foregroundColor(color) + Text(verbatim: ",\n").foregroundColor(punctuation)
            + key(Glyph.keyServer) + serverValue + Text(verbatim: ",\n").foregroundColor(punctuation)
            + key(Glyph.keyRouting) + Text(verbatim: "\"\(routing.wire)\"").foregroundColor(TerminalColors.syntaxString)
            + Text(verbatim: "\n}").foregroundColor(punctuation)
    }
}

private struct TrafficPanel: View {
    let traffic: TrafficStats
    let history: [TrafficPoint]
    let unit: TrafficUnit

    var body: some View {
        let download = TrafficFormat.shared.rate(bytesPerSecond: traffic.downloadSpeed, unit: unit)
        let upload = TrafficFormat.shared.rate(bytesPerSecond: traffic.uploadSpeed, unit: unit)
        VStack(alignment: .leading, spacing: TerminalSpacing.space10) {
            HStack {
                TerminalNote(String(localized: "traffic · 60 s"))
                Spacer()
                TerminalChartLegend(download: String(localized: "Download"), upload: String(localized: "Upload"))
            }
            TerminalTrafficChart(
                history.map { TerminalTrafficPoint(up: Double($0.uploadSpeed), down: Double($0.downloadSpeed)) },
                label: String(localized: "Download \(download), upload \(upload)")
            )
            Grid(horizontalSpacing: TerminalSpacing.space8, verticalSpacing: TerminalSpacing.space8) {
                GridRow {
                    StatCell(glyph: TerminalGlyph.download, label: String(localized: "Download"), value: download, color: TerminalColors.syntaxKey)
                    StatCell(glyph: TerminalGlyph.upload, label: String(localized: "Upload"), value: upload, color: TerminalColors.syntaxNumber)
                }
                GridRow {
                    StatCell(glyph: nil, label: Glyph.tcp, value: String(traffic.tcpConnections), color: TerminalColors.textPrimary)
                    StatCell(glyph: nil, label: Glyph.udp, value: String(traffic.udpConnections), color: TerminalColors.textPrimary)
                }
            }
            if traffic.cover.isActive {
                CoverLine(cover: traffic.cover, unit: unit)
                    .frame(maxWidth: .infinity, alignment: .trailing)
            }
            TotalLine(traffic: traffic, unit: unit)
                .frame(maxWidth: .infinity, alignment: .trailing)
        }
        .padding(.horizontal, TerminalSpacing.space14)
        .padding(.top, TerminalSpacing.space12)
        .padding(.bottom, TerminalSpacing.space14)
    }
}

/// Cover connections and speeds, kept apart from the real traffic above.
private struct CoverLine: View {
    let cover: CoverStats
    let unit: TrafficUnit

    var body: some View {
        let download = TrafficFormat.shared.rate(bytesPerSecond: cover.downloadSpeed, unit: unit)
        let upload = TrafficFormat.shared.rate(bytesPerSecond: cover.uploadSpeed, unit: unit)
        let connections = Int(cover.connections)
        (Text("Cover").foregroundColor(TerminalColors.textSecondary)
            + Text(verbatim: " ")
            + Text("\(connections) conn").foregroundColor(TerminalColors.textPrimary)
            + Text(verbatim: Glyph.footerSeparator).foregroundColor(TerminalColors.textSecondary)
            + Text(verbatim: "\(TerminalGlyph.download) ").foregroundColor(TerminalColors.syntaxKey)
            + Text(verbatim: download).foregroundColor(TerminalColors.textPrimary)
            + Text(verbatim: Glyph.footerSeparator).foregroundColor(TerminalColors.textSecondary)
            + Text(verbatim: "\(TerminalGlyph.upload) ").foregroundColor(TerminalColors.syntaxNumber)
            + Text(verbatim: upload).foregroundColor(TerminalColors.textPrimary))
            .font(TerminalType.caption)
            .lineLimit(1)
            .accessibilityElement()
            .accessibilityLabel(Text("Cover traffic: \(connections) connections, download \(download), upload \(upload)"))
    }
}

/// Upload and download of every connection so far, at the panel's bottom right.
private struct TotalLine: View {
    let traffic: TrafficStats
    let unit: TrafficUnit

    var body: some View {
        let download = TrafficFormat.shared.amount(bytes: traffic.cumulativeDownload, unit: unit)
        let upload = TrafficFormat.shared.amount(bytes: traffic.cumulativeUpload, unit: unit)
        (Text("Total").foregroundColor(TerminalColors.textSecondary)
            + Text(verbatim: " \(TerminalGlyph.download) ").foregroundColor(TerminalColors.syntaxKey)
            + Text(verbatim: download).foregroundColor(TerminalColors.textPrimary)
            + Text(verbatim: Glyph.footerSeparator).foregroundColor(TerminalColors.textSecondary)
            + Text(verbatim: "\(TerminalGlyph.upload) ").foregroundColor(TerminalColors.syntaxNumber)
            + Text(verbatim: upload).foregroundColor(TerminalColors.textPrimary))
            .font(TerminalType.caption)
            .lineLimit(1)
            .accessibilityElement()
            .accessibilityLabel(Text("Total downloaded \(download), uploaded \(upload)"))
    }
}

/// One line under the page: the tunnel extension's memory while connected,
/// then the iOS version, device model and app version.
private struct SystemFooter: View {
    let memory: MemoryUsage?

    var body: some View {
        let system = Text(verbatim: Self.systemLine).foregroundColor(TerminalColors.textSecondary)
        Group {
            if let memory {
                let used = TrafficFormat.shared.amount(bytes: memory.usedBytes, unit: .bytes)
                let limit = memory.limitBytes.map { TrafficFormat.shared.amount(bytes: $0.int64Value, unit: .bytes) }
                let value = limit.map { "\(used) / \($0)" } ?? used
                (Text(verbatim: "\(Glyph.memory) ").foregroundColor(TerminalColors.textSecondary)
                    + Text(verbatim: value).foregroundColor(memory.pressure.color)
                    + Text(verbatim: Glyph.footerSeparator).foregroundColor(TerminalColors.textSecondary)
                    + system)
                    .accessibilityElement()
                    .accessibilityLabel(Text(limit.map { "Network extension memory \(used) of \($0)" } ?? "Network extension memory \(used)")
                        + Text(verbatim: ", \(Self.systemLine)"))
            } else {
                system
            }
        }
        .font(TerminalType.caption)
        .lineLimit(1)
        .minimumScaleFactor(0.8)
    }

    private static let systemLine: String = {
        let device = UIDevice.current
        let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? ""
        return ["\(device.systemName) \(device.systemVersion)", modelIdentifier, "v\(version)"]
            .joined(separator: Glyph.footerSeparator)
    }()

    /// "iPhone17,3"; the Simulator reports the model it simulates.
    private static var modelIdentifier: String {
        if let simulated = ProcessInfo.processInfo.environment["SIMULATOR_MODEL_IDENTIFIER"] { return simulated }
        var system = utsname()
        uname(&system)
        return withUnsafeBytes(of: &system.machine) { String(decoding: $0.prefix { $0 != 0 }, as: UTF8.self) }
    }
}

private extension MemoryPressure {
    var color: Color {
        switch self {
        case .normal: TerminalColors.textSecondary
        case .elevated: TerminalColors.warning
        case .critical: TerminalColors.danger
        }
    }
}

/// Label over a value; a rate's unit is smaller and dimmed.
private struct StatCell: View {
    let glyph: String?
    let label: String
    let value: String
    let color: Color

    var body: some View {
        let (number, unit) = TerminalValueFormat.split(value)
        VStack(alignment: .leading, spacing: TerminalSpacing.space2) {
            HStack(spacing: 0) {
                if let glyph { TerminalGlyphText(glyph + " ", font: TerminalType.caption) }
                Text(label).font(TerminalType.caption).foregroundStyle(TerminalColors.textSecondary)
            }
            (Text(verbatim: number).foregroundColor(color) + Text(verbatim: unit.isEmpty ? "" : " \(unit)").foregroundColor(TerminalColors.textSecondary))
                .font(TerminalType.stat)
                .lineLimit(1)
                .minimumScaleFactor(0.7)
        }
        .padding(.horizontal, TerminalSpacing.space10)
        .padding(.vertical, TerminalSpacing.space8)
        .frame(maxWidth: .infinity, alignment: .leading)
        .overlay(RoundedRectangle(cornerRadius: TerminalRadius.md, style: .continuous).strokeBorder(TerminalColors.border))
        .accessibilityElement(children: .combine)
    }
}

/// A row of the home card: a short label, a value, and an optional colored status.
private struct NavRow: View {
    let label: String
    let title: String?
    var subtitle: String?
    var trailing: (String, Color)?

    var body: some View {
        HStack(spacing: TerminalSpacing.space12) {
            Text(label).font(TerminalType.small).foregroundStyle(TerminalColors.textSecondary).frame(width: 96, alignment: .leading)
            VStack(alignment: .leading, spacing: TerminalSpacing.space2) {
                if let title {
                    Text(title).font(TerminalType.bodyMedium).foregroundStyle(TerminalColors.textPrimary).lineLimit(1)
                }
                if let subtitle {
                    Text(verbatim: subtitle).font(TerminalType.label).foregroundStyle(TerminalColors.textSecondary).lineLimit(1)
                }
            }
            Spacer(minLength: 0)
            if let trailing {
                Text(trailing.0).font(TerminalType.small).foregroundStyle(trailing.1).lineLimit(1)
            }
            TerminalGlyphText(TerminalGlyph.next)
        }
        .padding(.horizontal, TerminalSpacing.space14)
        .padding(.vertical, TerminalSpacing.space10)
        .frame(minHeight: TerminalSize.row)
        .contentShape(Rectangle())
    }
}

/// The home page: `TerminalPage`'s layout plus a one-line `footer` that sits in the bottom
/// safe-area inset, just above the home indicator, and scrolls with the page.
/// The library page has no slot in the inset, so the home screen keeps this one.
private struct HomePage<Content: View, Bottom: View, Footer: View>: View {
    @ViewBuilder var content: Content
    @ViewBuilder var bottom: Bottom
    @ViewBuilder var footer: Footer

    /// Leaves the lower part of the 34 pt home indicator inset free.
    private static var footerHeight: CGFloat { TerminalSpacing.space20 }

    var body: some View {
        GeometryReader { geometry in
            let inset = geometry.safeAreaInsets.bottom
            ScrollView {
                VStack(spacing: 0) {
                    VStack(alignment: .leading, spacing: TerminalSpacing.space20) {
                        content
                        Spacer(minLength: 0)
                        bottom
                    }
                    .padding(.horizontal, TerminalSpacing.space20)
                    .padding(.vertical, TerminalSpacing.space16)
                    .frame(maxWidth: .infinity, minHeight: geometry.size.height, alignment: .topLeading)
                    footer
                        .padding(.horizontal, TerminalSpacing.space20)
                        .frame(maxWidth: .infinity, minHeight: Self.footerHeight)
                    // Keeps the end of the page above the home indicator when scrolled down.
                    Color.clear.frame(height: max(inset - Self.footerHeight, 0))
                }
            }
            .ignoresSafeArea(.container, edges: .bottom)
        }
        .background(TerminalColors.canvas.ignoresSafeArea())
        .foregroundStyle(TerminalColors.textPrimary)
    }
}
