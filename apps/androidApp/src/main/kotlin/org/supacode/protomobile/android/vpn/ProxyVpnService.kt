package org.supacode.protomobile.android.vpn

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.net.VpnService
import android.os.Build
import android.os.Handler
import android.os.Looper
import org.supacode.protomobile.android.JsonProxyApp
import org.supacode.protomobile.android.MainActivity
import org.supacode.protomobile.android.R
import org.supacode.protomobile.android.platform.AppPlatform
import org.supacode.protomobile.android.platform.logFromGo
import org.supacode.protomobile.core.mobile.Mobile
import org.supacode.protomobile.core.mobile.Platform
import org.supacode.protomobile.shared.AppLog
import org.supacode.protomobile.shared.VpnStatus

/**
 * Owns the TUN interface and the Go engine. Started by the app to connect,
 * or by the system for an always-on VPN, in which case the configuration is
 * rebuilt from the saved profile.
 */
class ProxyVpnService : VpnService() {
    private val main = Handler(Looper.getMainLooper())
    private var running = false
    private var networkCallback: ConnectivityManager.NetworkCallback? = null

    private val controller get() = (application as JsonProxyApp).graph.controller

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_DISCONNECT) {
            shutdown()
            return START_NOT_STICKY
        }
        startInForeground()
        // Without an extra this is an always-on start (or a restart after the
        // process died), so rebuild the configuration from storage.
        val config = intent?.getStringExtra(EXTRA_CONFIG) ?: controller.engineConfigForSelected()
        if (config == null) {
            shutdown()
            return START_NOT_STICKY
        }
        report(VpnStatus.Connecting)
        try {
            startEngine(config)
            report(VpnStatus.Connected)
        } catch (e: Exception) {
            AppLog.e(TAG, "VPN start failed: ${e.message}")
            shutdown(e.message ?: "VPN start failed")
        }
        return START_STICKY
    }

    override fun onRevoke() {
        // Another VPN took over or the user revoked the permission.
        shutdown()
    }

    override fun onDestroy() {
        if (running) stopEngine()
        super.onDestroy()
    }

    /**
     * Starts the engine on a new interface. When an engine already runs (the
     * configuration changed), the new interface is established first and the
     * old engine stopped after, so traffic never leaves the tunnel while
     * switching; stopping the old engine closes every flow it carried.
     */
    private fun startEngine(config: String) {
        val tun = Builder()
            .setSession(getString(R.string.app_name))
            .setMtu(MTU)
            // The Go core defines the interface addresses and refuses flows to them.
            .addAddress(Mobile.TunnelIPv4Address, Mobile.TunnelIPv4PrefixLength.toInt())
            .addRoute("0.0.0.0", 0)
            .addAddress(Mobile.TunnelIPv6Address, Mobile.TunnelIPv6PrefixLength.toInt())
            .addRoute("::", 0)
            // Any address works: the engine answers all DNS on port 53.
            .addDnsServer(Mobile.TunnelDNSAddress)
            // The app's own sockets (TCP ping, engine) stay outside the tunnel.
            .addDisallowedApplication(packageName)
            .setConfigureIntent(openAppIntent())
            .establish() ?: throw IllegalStateException("VPN permission was revoked")
        if (running) stopEngine()
        Mobile.setPlatform(EnginePlatform())
        // The Go engine takes ownership of the descriptor and closes it.
        Mobile.startEngine(tun.detachFd().toLong(), config)
        running = true
        registerNetworkCallback()
    }

    private fun stopEngine() {
        unregisterNetworkCallback()
        Mobile.stopEngine()
        Mobile.setPlatform(AppPlatform)
        running = false
    }

    private fun shutdown(error: String? = null) {
        if (running) stopEngine()
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
        report(VpnStatus.Disconnected, error)
    }

    private fun report(status: VpnStatus, error: String? = null) {
        main.post { controller.onVpnStatus(status, error) }
    }

    /**
     * Watches the physical networks. The first active network is the one the
     * VPN started on; a different one later is a switch, which clears DNS
     * caches and closes every flow so apps reconnect over the new network.
     * (The default-network callback stops firing for an app excluded from
     * its own VPN, so every non-VPN network event re-checks the active one.)
     */
    private fun registerNetworkCallback() {
        val connectivity = getSystemService(ConnectivityManager::class.java)
        val request = NetworkRequest.Builder()
            .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
            .build()
        val callback = object : ConnectivityManager.NetworkCallback() {
            private var current: Network? = connectivity.activeNetwork

            override fun onAvailable(network: Network) = check()
            override fun onLost(network: Network) = check()

            private fun check() {
                val active = connectivity.activeNetwork ?: return
                val previous = current
                current = active
                if (previous != null && previous != active) {
                    AppLog.i(TAG, "Network switched $previous -> $active")
                    Mobile.networkChanged()
                }
            }
        }
        connectivity.registerNetworkCallback(request, callback)
        networkCallback = callback
    }

    private fun unregisterNetworkCallback() {
        networkCallback?.let { getSystemService(ConnectivityManager::class.java).unregisterNetworkCallback(it) }
        networkCallback = null
    }

    private fun startInForeground() {
        val manager = getSystemService(NotificationManager::class.java)
        manager.createNotificationChannel(
            NotificationChannel(CHANNEL_ID, getString(R.string.notification_channel), NotificationManager.IMPORTANCE_LOW),
        )
        val disconnect = PendingIntent.getService(
            this, 1,
            Intent(this, ProxyVpnService::class.java).setAction(ACTION_DISCONNECT),
            PendingIntent.FLAG_IMMUTABLE,
        )
        val notification = Notification.Builder(this, CHANNEL_ID)
            .setSmallIcon(android.R.drawable.ic_lock_lock)
            .setContentTitle(getString(R.string.app_name))
            .setContentText(getString(R.string.notification_text))
            .setContentIntent(openAppIntent())
            .setOngoing(true)
            .addAction(Notification.Action.Builder(null, getString(R.string.action_disconnect), disconnect).build())
            .build()
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            startForeground(NOTIFICATION_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_SYSTEM_EXEMPTED)
        } else {
            startForeground(NOTIFICATION_ID, notification)
        }
    }

    private fun openAppIntent(): PendingIntent = PendingIntent.getActivity(
        this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE,
    )

    /** Engine callbacks while the VPN runs. */
    private inner class EnginePlatform : Platform {
        override fun protect(fd: Long): Boolean = this@ProxyVpnService.protect(fd.toInt())
        override fun log(level: Long, tag: String?, message: String?) = logFromGo(level, tag, message)
        override fun notice(kind: String?, message: String?) {
            if (kind == null) return
            main.post { controller.onEngineNotice(kind, message.orEmpty()) }
        }
    }

    companion object {
        const val ACTION_CONNECT = "org.supacode.protomobile.android.CONNECT"
        const val ACTION_DISCONNECT = "org.supacode.protomobile.android.DISCONNECT"
        const val EXTRA_CONFIG = "engine_config"
        private const val TAG = "ProxyVpnService"
        private const val CHANNEL_ID = "vpn"
        private const val NOTIFICATION_ID = 1
        private const val MTU = 1500
    }
}
