package org.supacode.protomobile.android

import android.app.Application
import org.supacode.protomobile.android.platform.AppPlatform
import org.supacode.protomobile.android.platform.GoCoreBridge
import org.supacode.protomobile.android.platform.KeystoreSecretStore
import org.supacode.protomobile.android.platform.PrefsStore
import org.supacode.protomobile.android.vpn.AndroidVpnBackend
import org.supacode.protomobile.core.mobile.Mobile
import org.supacode.protomobile.shared.AppGraph
import java.io.File

class JsonProxyApp : Application() {
    lateinit var graph: AppGraph
        private set

    override fun onCreate() {
        super.onCreate()
        // The VPN service replaces this while it runs so engine sockets are protected.
        Mobile.setPlatform(AppPlatform)
        graph = AppGraph(
            store = PrefsStore(this),
            secrets = KeystoreSecretStore(this),
            core = GoCoreBridge,
            backend = AndroidVpnBackend(this),
            // The engine runs in the app process; Android does not cap it like iOS.
            memoryLimitMb = 0,
            totalsFile = File(filesDir, "traffic-totals.json").path,
        )
    }
}
