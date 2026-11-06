import SupaProtoCore
import Network
import os
@preconcurrency import NetworkExtension

/// Runs the Go engine on the utun interface. The app stores the engine
/// configuration in the shared keychain before starting the tunnel, so the
/// system can also start it on demand without the app.
final class PacketTunnelProvider: NEPacketTunnelProvider {
    private let platform = TunnelPlatform()
    private var pathMonitor: NWPathMonitor?

    override func startTunnel(options: [String: NSObject]?, completionHandler: @escaping (Error?) -> Void) {
        guard let config = SharedKeychain.get(SharedConfig.engineConfigAccount) else {
            completionHandler(TunnelError.missingConfiguration)
            return
        }
        setTunnelNetworkSettings(Self.networkSettings()) { error in
            if let error {
                completionHandler(error)
                return
            }
            completionHandler(self.startEngine(config))
        }
    }

    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        pathMonitor?.cancel()
        pathMonitor = nil
        MobileStopEngine()
        completionHandler()
    }

    /// Answers the app's stats requests with the engine's JSON snapshot, and
    /// restarts the engine in place when the configuration changed.
    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)?) {
        let reply: String
        switch String(decoding: messageData, as: UTF8.self) {
        case SharedConfig.statsMessage: reply = Self.addingMemory(to: MobileStats(false))
        case SharedConfig.statsDetailMessage: reply = Self.addingMemory(to: MobileStats(true))
        case SharedConfig.reloadMessage: reply = reloadEngine()
        default: reply = ""
        }
        completionHandler?(reply.isEmpty ? nil : Data(reply.utf8))
    }

    /// Adds the extension's memory to a stats snapshot: the footprint the
    /// system compares with its limit, and how much more it may use.
    private static func addingMemory(to stats: String) -> String {
        guard var object = (try? JSONSerialization.jsonObject(with: Data(stats.utf8))) as? [String: Any],
              let used = physicalFootprint()
        else { return stats }
        object["memory"] = ["used": used, "available": Int64(os_proc_available_memory())]
        guard let data = try? JSONSerialization.data(withJSONObject: object) else { return stats }
        return String(decoding: data, as: UTF8.self)
    }

    private static func physicalFootprint() -> Int64? {
        var info = task_vm_info_data_t()
        var count = mach_msg_type_number_t(MemoryLayout<task_vm_info_data_t>.size / MemoryLayout<integer_t>.size)
        let result = withUnsafeMutablePointer(to: &info) {
            $0.withMemoryRebound(to: integer_t.self, capacity: Int(count)) {
                task_info(mach_task_self_, task_flavor_t(TASK_VM_INFO), $0, &count)
            }
        }
        return result == KERN_SUCCESS ? Int64(info.phys_footprint) : nil
    }

    /// Replaces the engine with one built from the stored configuration. The
    /// tunnel and its routes stay up, so traffic waits in the interface instead
    /// of leaving it; stopping the old engine closes every flow it carried.
    private func reloadEngine() -> String {
        guard let config = SharedKeychain.get(SharedConfig.engineConfigAccount) else {
            return TunnelError.missingConfiguration.localizedDescription
        }
        MobileStopEngine()
        if let error = startEngine(config) {
            // Without an engine the tunnel would only drop traffic.
            cancelTunnelWithError(error)
            return error.localizedDescription
        }
        return SharedConfig.reloadSucceeded
    }

    /// Starts the Go engine on the utun descriptor; returns the failure, if any.
    private func startEngine(_ config: String) -> Error? {
        pathMonitor?.cancel()
        pathMonitor = nil
        MobileSetPlatform(platform)
        var fd = 0
        var error: NSError?
        guard MobileTunnelFD(&fd, &error) else {
            return error ?? TunnelError.noTunnelDescriptor
        }
        guard MobileStartEngine(fd, config, &error) else {
            return error ?? TunnelError.engineFailed
        }
        // A switch between Wi-Fi and cellular clears DNS caches and closes every
        // flow so apps reconnect over the new network. The first update only
        // reports the network the tunnel started on.
        let monitor = NWPathMonitor(prohibitedInterfaceTypes: [.other])
        let lastNetwork = NetworkSignature()
        monitor.pathUpdateHandler = { path in
            if lastNetwork.update(path) { MobileNetworkChanged() }
        }
        monitor.start(queue: DispatchQueue(label: "org.supacode.protomobile.path"))
        pathMonitor = monitor
        return nil
    }

    /// The Go core defines the interface addresses, shared with Android, and
    /// refuses flows to them; it answers DNS sent to any address.
    private static func networkSettings() -> NEPacketTunnelNetworkSettings {
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "127.0.0.1")
        let ipv4 = NEIPv4Settings(
            addresses: [MobileTunnelIPv4Address],
            subnetMasks: [ipv4Mask(prefixLength: Int(MobileTunnelIPv4PrefixLength))]
        )
        ipv4.includedRoutes = [NEIPv4Route.default()]
        settings.ipv4Settings = ipv4
        let ipv6 = NEIPv6Settings(
            addresses: [MobileTunnelIPv6Address],
            networkPrefixLengths: [NSNumber(value: Int(MobileTunnelIPv6PrefixLength))]
        )
        ipv6.includedRoutes = [NEIPv6Route.default()]
        settings.ipv6Settings = ipv6
        let dns = NEDNSSettings(servers: [MobileTunnelDNSAddress])
        dns.matchDomains = [""]
        settings.dnsSettings = dns
        settings.mtu = 1500
        return settings
    }
}

/// Remembers the physical network the tunnel runs over. Only used from the
/// path monitor's serial queue.
private final class NetworkSignature: @unchecked Sendable {
    private var current: String?

    /// Records the path and returns true when it is a different network.
    func update(_ path: NWPath) -> Bool {
        let interfaces = path.availableInterfaces.map(\.name).joined(separator: ",")
        let signature = "\(path.status):\(interfaces)"
        defer { current = signature }
        return current != nil && current != signature
    }
}

/// Dotted subnet mask for an IPv4 prefix length, such as 255.255.255.252 for 30.
private func ipv4Mask(prefixLength: Int) -> String {
    let mask = prefixLength == 0 ? UInt32(0) : UInt32.max << (32 - prefixLength)
    return (0..<4).map { String((mask >> (24 - 8 * $0)) & 0xFF) }.joined(separator: ".")
}

enum TunnelError: LocalizedError {
    case missingConfiguration
    case noTunnelDescriptor
    case engineFailed

    var errorDescription: String? {
        switch self {
        case .missingConfiguration: String(localized: "No tunnel configuration; connect from the app first")
        case .noTunnelDescriptor: String(localized: "The tunnel interface was not found")
        case .engineFailed: String(localized: "The network engine failed to start")
        }
    }
}
