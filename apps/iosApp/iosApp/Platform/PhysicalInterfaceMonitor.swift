import SupaProtoCore
import Network
import OSLog

/// Keeps this app's own Go sockets (TCP ping and its bootstrap DNS lookups)
/// on the current Wi-Fi or cellular interface. iOS does not exclude the app
/// from its own tunnel, so unbound sockets would enter the VPN.
final class PhysicalInterfaceMonitor {
    private let monitor = NWPathMonitor(prohibitedInterfaceTypes: [.other])
    private static let logger = Logger(subsystem: "org.supacode.protomobile.app", category: "network")

    init() {
        monitor.pathUpdateHandler = { path in
            let physical: [NWInterface.InterfaceType] = [.wifi, .cellular, .wiredEthernet]
            let name = path.availableInterfaces.first { physical.contains($0.type) }?.name ?? ""
            var error: NSError?
            if !MobileSetPhysicalInterface(name, &error), let error {
                Self.logger.error("Cannot bind to \(name, privacy: .public): \(error.localizedDescription, privacy: .public)")
            }
        }
        monitor.start(queue: DispatchQueue(label: "org.supacode.protomobile.interface"))
    }

    deinit {
        monitor.cancel()
    }
}
