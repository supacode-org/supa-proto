import Foundation
import NetworkExtension
import Shared

/// Starts and stops the packet tunnel through NETunnelProviderManager and
/// reports its status to the shared controller. Kotlin calls start and stop
/// from any thread; all state is touched on the main actor.
final class TunnelBackend: VpnBackend, @unchecked Sendable {
    private var graph: AppGraph?
    private var manager: NETunnelProviderManager?
    private var statusObserver: NSObjectProtocol?
    private var noticeRelay: NoticeRelay?
    private var lastStatus: NEVPNStatus = .invalid
    /// Configuration to start once the tunnel finishes stopping for a full restart.
    private var pendingRestart: String?

    @MainActor
    func attach(_ graph: AppGraph) {
        self.graph = graph
        statusObserver = NotificationCenter.default.addObserver(
            forName: .NEVPNStatusDidChange, object: nil, queue: .main
        ) { [weak self] note in
            guard let status = (note.object as? NEVPNConnection)?.status else { return }
            MainActor.assumeIsolated { self?.report(status) }
        }
        noticeRelay = NoticeRelay { kind, message in
            graph.controller.onEngineNotice(kind: kind, detail: message)
        }
        Task { await refreshStatus() }
    }

    func start(engineConfigJson: String) {
        Task { @MainActor in await self.startTunnel(engineConfigJson) }
    }

    func stop() {
        Task { @MainActor in await self.stopTunnel() }
    }

    func restart(engineConfigJson: String) {
        Task { @MainActor in await self.restartTunnel(engineConfigJson) }
    }

    /// Asks the packet tunnel extension, where the engine runs, for its stats.
    func requestStats(detail: Bool, callback: @escaping (String?) -> Void) {
        let message = detail ? SharedConfig.statsDetailMessage : SharedConfig.statsMessage
        // A Kotlin lambda may be invoked from any thread.
        nonisolated(unsafe) let callback = callback
        Task { @MainActor in
            guard let session = self.manager?.connection as? NETunnelProviderSession,
                  session.status == .connected else {
                callback(nil)
                return
            }
            do {
                try session.sendProviderMessage(Data(message.utf8)) { response in
                    callback(response.map { String(decoding: $0, as: UTF8.self) })
                }
            } catch {
                callback(nil)
            }
        }
    }

    @MainActor
    private func startTunnel(_ config: String) async {
        guard SharedKeychain.set(config, account: SharedConfig.engineConfigAccount) else {
            fail("Could not store the tunnel configuration in the keychain")
            return
        }
        do {
            let manager = try await loadManager()
            configure(manager)
            try await save(manager)
            // Required after saving, or the first start fails.
            try await manager.loadFromPreferences()
            try manager.connection.startVPNTunnel()
        } catch where error.isVPNPermissionDenied {
            graph?.controller.onVpnStatus(status: .disconnected, error: nil)
            ToastCenter.shared.show(Toast(String(localized: "VPN permission denied"), duration: .long))
        } catch {
            fail(error.localizedDescription)
        }
    }

    /// Installs the VPN configuration if it does not exist yet; iOS asks the
    /// user the first time. Latency tests and other network features rely on
    /// it, so they call this first instead of failing silently without it.
    @MainActor
    func ensureConfiguration() async throws {
        let manager = try await loadManager()
        guard manager.protocolConfiguration == nil else { return }
        configure(manager)
        do {
            try await save(manager)
            try await manager.loadFromPreferences()
        } catch {
            // Start from a fresh manager next time instead of this unsaved one.
            self.manager = nil
            throw error
        }
    }

    /// Applies a new configuration. A connected tunnel swaps its engine in
    /// place, so traffic stays in the tunnel. On-demand and strict mode live in
    /// the VPN configuration itself and need a full stop and start.
    @MainActor
    private func restartTunnel(_ config: String) async {
        guard let manager, let session = manager.connection as? NETunnelProviderSession,
              session.status == .connected else {
            await startTunnel(config)
            return
        }
        if needsReconfiguration(manager) {
            pendingRestart = config
            await stopTunnel()
            return
        }
        guard SharedKeychain.set(config, account: SharedConfig.engineConfigAccount) else {
            fail("Could not store the tunnel configuration in the keychain")
            return
        }
        do {
            try session.sendProviderMessage(Data(SharedConfig.reloadMessage.utf8)) { [weak self] response in
                let reply = response.map { String(decoding: $0, as: UTF8.self) }
                Task { @MainActor in
                    if reply == SharedConfig.reloadSucceeded {
                        self?.graph?.controller.onVpnStatus(status: .connected, error: nil)
                    } else {
                        self?.fail(reply ?? "The tunnel did not answer")
                    }
                }
            }
        } catch {
            fail(error.localizedDescription)
        }
    }

    @MainActor
    private func needsReconfiguration(_ manager: NETunnelProviderManager) -> Bool {
        guard let settings = graph?.settings.current,
              let proto = manager.protocolConfiguration as? NETunnelProviderProtocol else { return true }
        return proto.includeAllNetworks != settings.strictKillSwitch || manager.isOnDemandEnabled != settings.autoConnect
    }

    @MainActor
    private func stopTunnel() async {
        guard let manager = try? await loadManager() else { return }
        if manager.isOnDemandEnabled {
            // Otherwise the system starts the tunnel again right away.
            manager.isOnDemandEnabled = false
            try? await manager.saveToPreferences()
        }
        manager.connection.stopVPNTunnel()
    }

    /// Saving is when iOS asks to add the configuration; a read/write failure
    /// there means the user declined. (Loading fails with the same code for
    /// other reasons, such as the missing NetworkExtension service in Simulator.)
    @MainActor
    private func save(_ manager: NETunnelProviderManager) async throws {
        do {
            try await manager.saveToPreferences()
        } catch let error as NSError
            where error.domain == NEVPNErrorDomain && error.code == NEVPNError.Code.configurationReadWriteFailed.rawValue {
            throw VPNPermissionDenied()
        }
    }

    @MainActor
    private func loadManager() async throws -> NETunnelProviderManager {
        if let manager { return manager }
        let managers = try await NETunnelProviderManager.loadAllFromPreferences()
        let manager = managers.first ?? NETunnelProviderManager()
        self.manager = manager
        return manager
    }

    @MainActor
    private func configure(_ manager: NETunnelProviderManager) {
        let settings = graph?.settings.current
        let proto = (manager.protocolConfiguration as? NETunnelProviderProtocol) ?? NETunnelProviderProtocol()
        proto.providerBundleIdentifier = SharedConfig.tunnelBundleID
        proto.serverAddress = graph?.profiles.selected?.server ?? "Supa Proto"
        proto.includeAllNetworks = settings?.strictKillSwitch ?? false
        proto.excludeLocalNetworks = true
        proto.disconnectOnSleep = false
        manager.protocolConfiguration = proto
        manager.localizedDescription = "Supa Proto"
        manager.isEnabled = true
        let onDemand = settings?.autoConnect ?? false
        manager.onDemandRules = onDemand ? [NEOnDemandRuleConnect()] : nil
        manager.isOnDemandEnabled = onDemand
    }

    @MainActor
    private func refreshStatus() async {
        guard let manager = try? await loadManager() else { return }
        report(manager.connection.status)
    }

    @MainActor
    private func report(_ status: NEVPNStatus) {
        guard let controller = graph?.controller else { return }
        let previous = lastStatus
        lastStatus = status
        switch status {
        case .connecting, .reasserting:
            controller.onVpnStatus(status: .connecting, error: nil)
        case .connected:
            controller.onVpnStatus(status: .connected, error: nil)
        case .disconnecting:
            controller.onVpnStatus(status: .disconnecting, error: nil)
        case .disconnected, .invalid:
            if let config = pendingRestart {
                // A full restart: the old tunnel is down, start the new one.
                pendingRestart = nil
                controller.onVpnStatus(status: .connecting, error: nil)
                Task { await startTunnel(config) }
                return
            }
            guard previous == .connecting, let connection = manager?.connection else {
                controller.onVpnStatus(status: .disconnected, error: nil)
                return
            }
            // The tunnel never came up: surface the extension's error.
            connection.fetchLastDisconnectError { [weak self] error in
                let message = error?.localizedDescription
                Task { @MainActor in
                    self?.graph?.controller.onVpnStatus(status: .disconnected, error: message)
                }
            }
        @unknown default:
            break
        }
    }

    @MainActor
    private func fail(_ message: String) {
        graph?.controller.onVpnStatus(status: .disconnected, error: message)
    }
}

/// Receives engine notices that the extension stores in the App Group and
/// announces with a Darwin notification.
@MainActor
final class NoticeRelay {
    private let handler: (String, String) -> Void

    init(handler: @escaping (String, String) -> Void) {
        self.handler = handler
        CFNotificationCenterAddObserver(
            CFNotificationCenterGetDarwinNotifyCenter(),
            Unmanaged.passUnretained(self).toOpaque(),
            { _, observer, _, _, _ in
                guard let observer else { return }
                let relay = Unmanaged<NoticeRelay>.fromOpaque(observer).takeUnretainedValue()
                Task { @MainActor in relay.deliver() }
            },
            SharedConfig.noticeNotification as CFString,
            nil,
            .deliverImmediately
        )
    }

    deinit {
        CFNotificationCenterRemoveEveryObserver(
            CFNotificationCenterGetDarwinNotifyCenter(),
            Unmanaged.passUnretained(self).toOpaque()
        )
    }

    private func deliver() {
        let defaults = SharedConfig.groupDefaults
        guard let kind = defaults.string(forKey: SharedConfig.noticeKindKey),
              let message = defaults.string(forKey: SharedConfig.noticeMessageKey) else { return }
        handler(kind, message)
    }
}

/// The user tapped "Don't Allow" when iOS asked to add the VPN configuration.
struct VPNPermissionDenied: Error {}

extension Error {
    var isVPNPermissionDenied: Bool { self is VPNPermissionDenied }
}
