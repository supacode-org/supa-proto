import OSLog
import Shared
import UIKit

/// Bridges the shared Kotlin graph to SwiftUI. SKIE exposes each Kotlin
/// StateFlow as an AsyncSequence, mirrored here into @Published values.
@MainActor
final class AppModel: ObservableObject {
    @Published private(set) var state: ConnectionState
    @Published private(set) var profiles: [ServerProfile] = []
    @Published private(set) var selectedId: String?
    @Published private(set) var pings: [String: PingState] = [:]
    @Published private(set) var settings: AppSettings
    /// Connection counts and speeds while connected.
    @Published private(set) var traffic: TrafficStats?
    /// Speeds of the last minute, oldest first, for the home chart.
    @Published private(set) var trafficHistory: [TrafficPoint] = []
    /// A server read from a scanned QR code or an opened link, waiting for the user to review it.
    @Published var pendingImport: ImportedServer?

    let platform: String
    let networkAccess = NetworkAccess()
    private let graph: AppGraph
    private let backend: TunnelBackend
    private let toasts: ToastCenter
    private let physicalInterface = PhysicalInterfaceMonitor()

    init(toasts: ToastCenter = .shared) {
        let backend = TunnelBackend()
        let graph = AppGraph(
            store: GroupDefaultsStore(),
            secrets: KeychainSecretStore(),
            core: GoCoreBridge(),
            backend: backend,
            memoryLimitMb: SharedConfig.extensionMemoryLimitMB,
            totalsFile: SharedConfig.trafficTotalsPath
        )
        self.graph = graph
        self.backend = backend
        self.toasts = toasts
        self.state = graph.controller.state.value
        self.settings = graph.settings.settings.value
        self.platform = graph.controller.platform
        backend.attach(graph)
    }

    var selectedProfile: ServerProfile? {
        profiles.first { $0.id == selectedId }
    }

    func observe() async {
        await withTaskGroup(of: Void.self) { group in
            group.addTask { await self.observeState() }
            group.addTask { await self.observeProfiles() }
            group.addTask { await self.observeSelection() }
            group.addTask { await self.observePings() }
            group.addTask { await self.observeSettings() }
            group.addTask { await self.observeNotices() }
            group.addTask { await self.observeTraffic() }
            group.addTask { await self.observeTrafficHistory() }
        }
    }

    private func observeState() async {
        for await value in graph.controller.state { state = value }
    }

    private func observeProfiles() async {
        for await value in graph.profiles.profiles { profiles = value }
    }

    private func observeSelection() async {
        for await value in graph.profiles.selectedId { selectedId = value }
    }

    private func observePings() async {
        for await value in graph.controller.pings { pings = value }
    }

    private func observeSettings() async {
        for await value in graph.settings.settings { settings = value }
    }

    private func observeTraffic() async {
        for await value in graph.controller.traffic { traffic = value }
    }

    private func observeTrafficHistory() async {
        for await value in graph.controller.trafficHistory { trafficHistory = value }
    }

    private func observeNotices() async {
        for await notice in graph.controller.notices {
            toasts.show(Toast(notice: notice))
        }
    }

    // MARK: Actions

    func connect() { graph.controller.connect() }
    func disconnect() { graph.controller.disconnect() }
    func select(_ id: String) { graph.profiles.select(id: id) }
    func delete(_ id: String) { graph.profiles.delete(id: id) }
    func ping(_ id: String) {
        Task { if await prepareVPN() { graph.controller.ping(profileId: id) } }
    }

    func pingAll() {
        Task { if await prepareVPN() { graph.controller.pingAll() } }
    }

    /// Latency tests need the VPN configuration; create it first (iOS asks the
    /// user once) and explain a refusal instead of failing silently.
    private static let logger = Logger(subsystem: "com.json2proxy.app", category: "vpn")

    private func prepareVPN() async -> Bool {
        #if targetEnvironment(simulator)
        // Simulator has no NetworkExtension service, so the configuration can never exist there.
        return true
        #else
        do {
            try await backend.ensureConfiguration()
            return true
        } catch where error.isVPNPermissionDenied {
            Self.logger.notice("VPN configuration refused by the user")
            toasts.show(Toast(String(localized: "Allow adding the VPN configuration to test latency."), duration: .long))
        } catch {
            Self.logger.error("Cannot create the VPN configuration: \(error.localizedDescription, privacy: .public)")
            toasts.show(Toast(String(localized: "Could not create the VPN configuration: \(error.localizedDescription)"), duration: .long))
        }
        return false
        #endif
    }
    func newProfileId() -> String { graph.profiles.generateId() }
    func hasSecret(_ id: String) -> Bool { graph.profiles.secret(id: id) != nil }

    /// Saves the profile when valid and returns true; otherwise reports the
    /// problems, or a toast when the keychain refuses the secret. A nil secret
    /// keeps the stored one.
    func save(_ profile: ServerProfile, secret: String?) -> (saved: Bool, problems: [ProfileProblem]) {
        let problems = ProfileValidator.shared.validate(profile: profile, secret: secret)
        guard problems.isEmpty else { return (false, problems) }
        guard graph.profiles.save(profile: profile, secret: secret) else {
            toasts.show(Toast(String(localized: "Could not store the secret securely. The server was not saved."), duration: .long))
            return (false, [])
        }
        return (true, [])
    }

    func update(_ settings: AppSettings) { graph.settings.update(settings: settings) }

    // MARK: Server links

    /// Handles a json2proxy:// link opened from another app or the system camera.
    func open(_ url: URL) {
        switch onEnum(of: ServerLinks.shared.parse(text: url.absoluteString)) {
        case .success(let result): pendingImport = ImportedServer(link: result.link)
        case .failure(let result): toasts.show(Toast(result.problem.localizedText, duration: .long))
        }
    }

    func presentScanner() {
        guard let host = UIApplication.shared.topViewController else { return }
        let scanner = QRCodeScannerViewController(
            hint: String(localized: "Point the camera at a JSON2Proxy QR code"),
            validator: { raw -> ScanDecision<ServerLink> in
                switch onEnum(of: ServerLinks.shared.parse(text: raw)) {
                case .success(let result): .accept(result.link)
                case .failure(let result): .reject(result.problem.localizedText)
                }
            },
            handler: { [weak self] link in self?.pendingImport = ImportedServer(link: link) }
        )
        scanner.present(from: host)
    }

    /// The shareable link for a saved server, or nil when its secret is missing.
    func link(for profile: ServerProfile) -> String? {
        guard let secret = graph.profiles.secret(id: profile.id) else { return nil }
        return ServerLinks.shared.format(profile: profile, secret: secret)
    }
    func validateRules(_ text: String) -> String? { graph.controller.validateRules(text: text) }
}

extension Toast {
    init(notice: any Notice) {
        switch notice.level {
        case .info: self.init(notice.localizedText, duration: .short)
        case .error: self.init(notice.localizedText, duration: .long)
        }
    }
}

// The Go core and shared code only supply details; all text is localized here.

extension Notice {
    var localizedText: String {
        switch onEnum(of: self) {
        case .connected(let notice): String(localized: "Connected to \(notice.detail)")
        case .connectionFailed(let notice): notice.reason.localizedText(detail: notice.detail, isToast: true)
        case .proxyUnavailable(let notice): String(localized: "Proxy unavailable: \(notice.detail)")
        case .bootstrapFailed(let notice): String(localized: "Bootstrap DNS failed: \(notice.detail)")
        case .dohUnavailable(let notice): String(localized: "DoH unavailable: \(notice.detail)")
        }
    }
}

extension FailureReason {
    func localizedText(detail: String, isToast: Bool) -> String {
        switch self {
        case .noServerSelected: String(localized: "Select a server first")
        case .missingSecret: String(localized: "No secret saved for \(detail)")
        case .error: isToast ? String(localized: "Connection failed: \(detail)") : String(localized: "Failed: \(detail)")
        }
    }
}

extension ConnectionState {
    var isActive: Bool {
        switch onEnum(of: self) {
        case .connecting, .connected, .disconnecting: true
        case .disconnected, .failed: false
        }
    }

    var label: String {
        switch onEnum(of: self) {
        case .disconnected: String(localized: "Disconnected")
        case .connecting(let state): String(localized: "Connecting to \(state.server)…")
        case .connected(let state): String(localized: "Connected to \(state.server)")
        case .disconnecting: String(localized: "Disconnecting…")
        case .failed(let state): state.reason.localizedText(detail: state.detail, isToast: false)
        }
    }
}

extension PingState {
    var label: String {
        switch onEnum(of: self) {
        case .testing: String(localized: "Testing…")
        case .success(let result):
            result.millis < 1 ? String(localized: "<1 ms") : String(localized: "\(result.millis) ms")
        case .failed(let result):
            result.detail.isEmpty ? String(localized: "Invalid address") : String(localized: "Unreachable")
        }
    }

    var isFailure: Bool {
        if case .failed = onEnum(of: self) { return true }
        return false
    }
}

struct ImportedServer: Identifiable {
    let id = UUID()
    let link: ServerLink
}

extension LinkProblem {
    var localizedText: String {
        switch self {
        case .notServerLink: String(localized: "Not a JSON2Proxy QR code or link")
        case .unsupportedVersion: String(localized: "This link needs a newer version of the app")
        case .missingCredentials: String(localized: "The link is missing the client ID or secret")
        case .invalidServer: String(localized: "The link has an invalid server address")
        case .invalidParameter: String(localized: "The link has an invalid setting")
        }
    }
}

extension UIApplication {
    /// The view controller currently on top of the foreground scene, for presenting UIKit screens.
    var topViewController: UIViewController? {
        let scene = connectedScenes
            .compactMap { $0 as? UIWindowScene }
            .first { $0.activationState == .foregroundActive }
        var top = scene?.windows.first { $0.isKeyWindow }?.rootViewController
        while let presented = top?.presentedViewController { top = presented }
        return top
    }
}

extension ProfileProblem {
    var localizedText: String {
        switch self {
        case .nameRequired: String(localized: "Name is required")
        case .invalidServer: String(localized: "Server must be host:port")
        case .clientIdRequired: String(localized: "Client ID is required")
        case .secretNotBase64Url: String(localized: "Secret must be unpadded base64url")
        case .secretTooShort: String(localized: "Secret must be at least 32 bytes")
        case .unknownObfsProvider: String(localized: "Unknown obfuscation provider")
        case .invalidParticipation: String(localized: "Participation must be between 0 and 1")
        case .unknownPerformanceMode: String(localized: "Unknown performance mode")
        case .invalidCoverRate:
            String(localized: "Target rate must be between \(Int(CoverTraffic.shared.MIN_RATE_KBPS)) and \(Int(CoverTraffic.shared.MAX_RATE_KBPS)) kbps")
        }
    }
}

/// Provider names are configuration values; only "none" is translated.
func obfsLabel(_ provider: String) -> String {
    provider == ObfsProviders.shared.NONE ? String(localized: "None") : provider
}

func performanceLabel(_ mode: String) -> String {
    switch mode {
    case PerformanceModes.shared.INTERACTIVE: String(localized: "Interactive")
    case PerformanceModes.shared.THROUGHPUT: String(localized: "Throughput")
    default: mode
    }
}
