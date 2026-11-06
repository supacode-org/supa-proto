package org.supacode.protomobile.android.vpn

import android.content.Context
import android.content.Intent
import org.supacode.protomobile.shared.VpnBackend
import org.supacode.protomobile.core.mobile.Mobile

/**
 * Starts and stops [ProxyVpnService]. The caller must already hold the VPN
 * permission (VpnService.prepare returned null).
 */
class AndroidVpnBackend(private val context: Context) : VpnBackend {
    override fun start(engineConfigJson: String) {
        val intent = Intent(context, ProxyVpnService::class.java)
            .setAction(ProxyVpnService.ACTION_CONNECT)
            .putExtra(ProxyVpnService.EXTRA_CONFIG, engineConfigJson)
        context.startForegroundService(intent)
    }

    override fun stop() {
        val intent = Intent(context, ProxyVpnService::class.java).setAction(ProxyVpnService.ACTION_DISCONNECT)
        context.startService(intent)
    }

    /** The running service swaps in a new interface and engine without leaving the tunnel. */
    override fun restart(engineConfigJson: String) = start(engineConfigJson)

    /** The engine runs in this process, so the Go core answers directly. */
    override fun requestStats(detail: Boolean, callback: (String?) -> Unit) {
        callback(Mobile.stats(detail).ifEmpty { null })
    }
}
