import Foundation
import SupaProtoCore
import Shared

/// Non-secret app data in the App Group, readable by the extension too.
final class GroupDefaultsStore: KeyValueStore {
    func getString(key: String) -> String? { SharedConfig.groupDefaults.string(forKey: key) }
    func putString(key: String, value: String) { SharedConfig.groupDefaults.set(value, forKey: key) }
    func remove(key: String) { SharedConfig.groupDefaults.removeObject(forKey: key) }
}

final class KeychainSecretStore: SecretStore {
    func getSecret(id: String) -> String? { SharedKeychain.get(account(id)) }
    func putSecret(id: String, secret: String) -> Bool { SharedKeychain.set(secret, account: account(id)) }
    func removeSecret(id: String) { SharedKeychain.delete(account(id)) }

    private func account(_ id: String) -> String { "secret.\(id)" }
}

/// Calls into the Go core linked into the app (TCP ping and rule checks).
final class GoCoreBridge: CoreBridge {
    func tcpPing(bootstrap: String, host: String, port: Int32, timeoutMillis: Int32, attempts: Int32) -> PingResult {
        guard let result = MobileTCPPing(bootstrap, host, Int(port), Int(timeoutMillis), Int(attempts)) else {
            return PingResult(address: "", resolveMillis: 0, connectMillis: 0, successes: 0, error: "Ping failed")
        }
        return PingResult(
            address: result.address,
            resolveMillis: result.resolveMillis,
            connectMillis: result.connectMillis,
            successes: Int32(result.successes),
            error: result.error
        )
    }

    func validateRules(text: String) -> String? {
        var error: NSError?
        MobileValidateRules(text, &error)
        return error?.localizedDescription
    }
}
