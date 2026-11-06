import Foundation
import SupaProtoCore
import os

/// Go core callbacks inside the extension. The Go runtime calls these from
/// its own threads, and the type keeps no mutable state.
final class TunnelPlatform: NSObject, MobilePlatformProtocol, @unchecked Sendable {
    private let logger = Logger(subsystem: SharedConfig.tunnelBundleID, category: "core")

    func log(_ level: Int, tag: String?, message: String?) {
        let text = "[\(tag ?? "")] \(message ?? "")"
        switch level {
        case 3: logger.error("\(text, privacy: .public)")
        case 2: logger.warning("\(text, privacy: .public)")
        case 1: logger.info("\(text, privacy: .public)")
        default: logger.debug("\(text, privacy: .public)")
        }
    }

    /// Hands the notice to the app: store it in the App Group, then post a
    /// Darwin notification that the app observes.
    func notice(_ kind: String?, message: String?) {
        let defaults = SharedConfig.groupDefaults
        defaults.set(kind ?? "", forKey: SharedConfig.noticeKindKey)
        defaults.set(message ?? "", forKey: SharedConfig.noticeMessageKey)
        CFNotificationCenterPostNotification(
            CFNotificationCenterGetDarwinNotifyCenter(),
            CFNotificationName(SharedConfig.noticeNotification as CFString),
            nil, nil, true
        )
    }

    /// Sockets opened by a packet tunnel provider never enter its own tunnel.
    func protect(_ fd: Int) -> Bool { true }
}
