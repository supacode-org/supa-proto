import Shared
import SwiftUI
import TerminalUI

struct ServerEditorView: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.dismiss) private var dismiss

    private let existing: ServerProfile?
    private let isImport: Bool
    @State private var name: String
    @State private var server: String
    @State private var clientId: String
    @State private var secret: String
    @State private var obfsProvider: String
    @State private var participation: String
    @State private var performanceMode: String
    @State private var udpEnabled: Bool
    @State private var coverEnabled: Bool
    @State private var coverRate: String
    @State private var problems: [ProfileProblem] = []

    /// Edits `profile`, or adds a server; `imported` pre-fills a new server from a scanned link.
    init(profile: ServerProfile?, imported: ServerLink? = nil) {
        existing = profile
        isImport = imported != nil
        let profile = imported?.profile ?? profile
        _secret = State(initialValue: imported?.secret ?? "")
        _name = State(initialValue: profile?.name ?? "")
        _server = State(initialValue: profile?.server ?? "")
        _clientId = State(initialValue: profile?.clientId ?? "")
        _obfsProvider = State(initialValue: profile?.obfsProvider ?? ObfsProviders.shared.NONE)
        _participation = State(initialValue: String(profile?.obfsParticipation ?? 0))
        _performanceMode = State(initialValue: profile?.performanceMode ?? PerformanceModes.shared.INTERACTIVE)
        _udpEnabled = State(initialValue: profile?.udpEnabled ?? true)
        _coverEnabled = State(initialValue: profile?.coverEnabled ?? false)
        _coverRate = State(initialValue: String(profile?.coverRateKbps ?? CoverTraffic.shared.DEFAULT_RATE_KBPS))
    }

    /// Parts of the form that a validation problem points at, in form order.
    private enum Field: Int, Comparable {
        case name, server, clientId, secret, obfs, participation, cover, transport

        static func < (lhs: Field, rhs: Field) -> Bool { lhs.rawValue < rhs.rawValue }
    }

    private func field(of problem: ProfileProblem) -> Field {
        switch problem {
        case .nameRequired: .name
        case .invalidServer: .server
        case .clientIdRequired: .clientId
        case .secretNotBase64Url, .secretTooShort: .secret
        case .unknownObfsProvider: .obfs
        case .invalidParticipation: .participation
        case .unknownPerformanceMode: .transport
        case .invalidCoverRate: .cover
        }
    }

    private func error(_ field: Field) -> String? {
        problems.first { self.field(of: $0) == field }?.localizedText
    }

    var body: some View {
        ScrollViewReader { reader in
            TerminalPage {
                TerminalPathTitle(String(localized: "servers"), existing == nil ? String(localized: "new") : String(localized: "edit"))
                if isImport {
                    TerminalBanner(String(localized: "Review the imported server, then save."), kind: .info)
                }

                VStack(alignment: .leading, spacing: TerminalSpacing.space14) {
                    TerminalTextField(String(localized: "Name"), text: $name, error: error(.name))
                        .id(Field.name)
                    TerminalTextField(String(localized: "Server (host:port)"), text: $server, error: error(.server))
                        .keyboardType(.URL)
                        .id(Field.server)
                    TerminalTextField(String(localized: "Client ID"), text: $clientId, error: error(.clientId))
                        .id(Field.clientId)
                    // The label sits above the field; an empty placeholder keeps it from repeating inside.
                    TerminalTextField(
                        existing == nil ? String(localized: "Secret") : String(localized: "Secret (leave empty to keep)"),
                        text: $secret,
                        placeholder: "",
                        error: error(.secret),
                        isSecure: true
                    )
                    .id(Field.secret)
                }

                TerminalSection(String(localized: "Obfuscation")) {
                    TerminalSegmented(
                        selection: $obfsProvider,
                        options: ObfsProviders.shared.all.map { TerminalOption($0, title: obfsLabel($0)) }
                    )
                    .onChange(of: obfsProvider) { provider in
                        participation = provider == ObfsProviders.shared.NONE
                            ? "0" : String(ObfsProviders.shared.DEFAULT_PARTICIPATION)
                    }
                    if let message = error(.obfs) { TerminalFieldError(message) }
                    // Participation only matters once a provider is chosen.
                    if obfsProvider != ObfsProviders.shared.NONE || error(.participation) != nil {
                        TerminalTextField(String(localized: "Participation (0–1)"), text: $participation, error: error(.participation))
                            .keyboardType(.decimalPad)
                            .id(Field.participation)
                    }
                }
                .id(Field.obfs)

                TerminalSection(String(localized: "Cover traffic")) {
                    let supported = ObfsProviders.shared.supportsCover(provider: obfsProvider)
                    TerminalSwitchRow(
                        String(localized: "Send cover traffic"),
                        isOn: Binding(get: { coverEnabled && supported }, set: { coverEnabled = $0 })
                    )
                    .disabled(!supported)
                    if !supported {
                        TerminalNote(String(localized: "Choose an obfuscation provider to use cover traffic."))
                    } else if coverEnabled {
                        TerminalTextField(String(localized: "Target rate (kbps)"), text: $coverRate, error: error(.cover))
                            .keyboardType(.numberPad)
                        if let rate = validCoverRate {
                            TerminalNote(String(localized: "About \(CoverTraffic.shared.monthlyEstimate(rateKbps: rate)) per month if connected all day; actual use depends on time connected."))
                        }
                    }
                }
                .id(Field.cover)

                TerminalSection(String(localized: "Transport")) {
                    TerminalChoiceList(
                        selection: $performanceMode,
                        options: PerformanceModes.shared.all.map { TerminalOption($0, title: performanceLabel($0), detail: $0) }
                    )
                    if let message = error(.transport) { TerminalFieldError(message) }
                    TerminalSwitchRow(String(localized: "UDP"), isOn: $udpEnabled)
                }
                .id(Field.transport)
            }
            .onChange(of: problems) { problems in
                guard let first = problems.map(field(of:)).min() else { return }
                withAnimation { reader.scrollTo(first, anchor: .top) }
            }
        }
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .cancellationAction) {
                Button { dismiss() } label: { Label { Text("Cancel") } icon: { TerminalIcon.close.image } }
                    .labelStyle(.iconOnly)
            }
            ToolbarItem(placement: .confirmationAction) {
                Button("Save", action: save)
                    .font(TerminalType.bodyBold)
                    .foregroundStyle(TerminalColors.accentText)
            }
        }
    }

    /// The entered rate when it is within the bounds the core accepts.
    private var validCoverRate: Int32? {
        guard let rate = Int32(ModelsKt.normalizeAscii(input: coverRate)),
              (CoverTraffic.shared.MIN_RATE_KBPS...CoverTraffic.shared.MAX_RATE_KBPS).contains(rate) else { return nil }
        return rate
    }

    private func save() {
        let profile = ServerProfile(
            id: existing?.id ?? model.newProfileId(),
            name: name.trimmingCharacters(in: .whitespaces),
            server: ModelsKt.normalizeAscii(input: server),
            clientId: ModelsKt.normalizeAscii(input: clientId),
            obfsProvider: obfsProvider,
            obfsParticipation: Double(ModelsKt.normalizeAscii(input: participation)) ?? .nan,
            performanceMode: performanceMode,
            udpEnabled: udpEnabled,
            // A provider without a cover profile cannot carry cover traffic.
            coverEnabled: coverEnabled && ObfsProviders.shared.supportsCover(provider: obfsProvider),
            coverRateKbps: Int32(ModelsKt.normalizeAscii(input: coverRate)) ?? -1
        )
        // Editing with an empty field keeps the stored secret.
        let newSecret: String? = (existing != nil && secret.isEmpty) ? nil : ModelsKt.normalizeAscii(input: secret)
        let result = model.save(profile, secret: newSecret)
        problems = result.problems
        if result.saved { dismiss() }
    }
}
