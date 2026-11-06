package org.supacode.protomobile.shared

/** Plain key-value storage for non-secret data (SharedPreferences, App Group defaults). */
interface KeyValueStore {
    fun getString(key: String): String?
    fun putString(key: String, value: String)
    fun remove(key: String)
}

/** Storage for server secrets (Android Keystore, iOS Keychain). */
interface SecretStore {
    fun getSecret(id: String): String?
    /** Returns false when the secret could not be stored. */
    fun putSecret(id: String, secret: String): Boolean
    fun removeSecret(id: String)
}

data class PingResult(
    val address: String,
    val resolveMillis: Long,
    val connectMillis: Long,
    val successes: Int,
    val error: String,
)

/** Calls into the Go core (gomobile) that the app process needs. */
interface CoreBridge {
    /** Blocking; run off the main thread. */
    fun tcpPing(bootstrap: String, host: String, port: Int, timeoutMillis: Int, attempts: Int): PingResult

    /** Returns null when the rules are valid, otherwise the first error. */
    fun validateRules(text: String): String?
}

enum class VpnStatus { Disconnected, Connecting, Connected, Disconnecting }

/**
 * Starts and stops the system VPN. Implementations report progress back
 * through [ProxyController.onVpnStatus] and engine events through
 * [ProxyController.onEngineNotice].
 */
interface VpnBackend {
    fun start(engineConfigJson: String)
    fun stop()

    /**
     * Applies a new configuration to the running VPN. Implementations keep
     * traffic inside the tunnel while switching where the platform allows it.
     */
    fun restart(engineConfigJson: String)

    /**
     * Asks the running engine for its stats JSON (Go `Stats`), with hosts,
     * DNS results and flows when [detail] is set. [callback] gets null when
     * the engine is not running or cannot be reached; it may run on any thread.
     */
    fun requestStats(detail: Boolean, callback: (String?) -> Unit)
}
