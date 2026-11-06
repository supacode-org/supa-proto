package org.supacode.protomobile.android.platform

import android.util.Log
import org.supacode.protomobile.core.mobile.Mobile
import org.supacode.protomobile.core.mobile.Platform
import org.supacode.protomobile.shared.CoreBridge
import org.supacode.protomobile.shared.PingResult

/** Go core calls made from the app process (TCP ping, rule checks). */
object GoCoreBridge : CoreBridge {
    override fun tcpPing(bootstrap: String, host: String, port: Int, timeoutMillis: Int, attempts: Int): PingResult {
        val result = Mobile.tcpPing(bootstrap, host, port.toLong(), timeoutMillis.toLong(), attempts.toLong())
            ?: return PingResult("", 0, 0, 0, "Ping failed")
        return PingResult(
            address = result.address,
            resolveMillis = result.resolveMillis,
            connectMillis = result.connectMillis,
            successes = result.successes.toInt(),
            error = result.error,
        )
    }

    override fun validateRules(text: String): String? = try {
        Mobile.validateRules(text)
        null
    } catch (e: Exception) {
        e.message ?: "Invalid rules"
    }
}

/** Go log levels: 0 debug, 1 info, 2 warn, 3 error. */
internal fun logFromGo(level: Long, tag: String?, message: String?) {
    val priority = when (level) {
        0L -> Log.DEBUG
        1L -> Log.INFO
        2L -> Log.WARN
        else -> Log.ERROR
    }
    Log.println(priority, "Go/${tag.orEmpty()}", message.orEmpty())
}

/**
 * Callbacks used while no VPN is running. The app excludes itself from its
 * own VPN, so its sockets never need protecting.
 */
object AppPlatform : Platform {
    override fun protect(fd: Long): Boolean = true
    override fun log(level: Long, tag: String?, message: String?) = logFromGo(level, tag, message)
    override fun notice(kind: String?, message: String?) = Unit
}
