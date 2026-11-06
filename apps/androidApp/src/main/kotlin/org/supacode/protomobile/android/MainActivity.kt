package org.supacode.protomobile.android

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.SystemBarStyle
import androidx.activity.enableEdgeToEdge
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.toArgb
import android.content.res.Configuration
import android.graphics.drawable.ColorDrawable
import org.supacode.protomobile.android.ui.JsonProxyUi
import org.supacode.terminalui.TerminalColorSchemes
import org.supacode.terminalui.TerminalTheme

class MainActivity : ComponentActivity() {
    /** A supaproto:// link waiting to be imported. */
    private var incomingLink by mutableStateOf<String?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        applyAppearance()
        // Recreating the activity redelivers the same intent; import it only once.
        if (savedInstanceState == null) incomingLink = intent.newLinkOrNull()
        val graph = (application as JsonProxyApp).graph
        setContent {
            TerminalTheme {
                JsonProxyUi(graph, incomingLink = incomingLink, onLinkHandled = { incomingLink = null })
            }
        }
    }

    // The manifest keeps the activity on uiMode changes, so the open page survives a switch of
    // the system appearance; Compose recolors itself, the window and system bars are redone here.
    override fun onConfigurationChanged(newConfig: Configuration) {
        super.onConfigurationChanged(newConfig)
        applyAppearance()
    }

    /** System bar icons and the window background for the current system appearance. */
    private fun applyAppearance() {
        enableEdgeToEdge(
            statusBarStyle = SystemBarStyle.auto(android.graphics.Color.TRANSPARENT, android.graphics.Color.TRANSPARENT),
            navigationBarStyle = SystemBarStyle.auto(android.graphics.Color.TRANSPARENT, android.graphics.Color.TRANSPARENT),
        )
        val night = resources.configuration.uiMode and Configuration.UI_MODE_NIGHT_MASK == Configuration.UI_MODE_NIGHT_YES
        val scheme = if (night) TerminalColorSchemes.dark else TerminalColorSchemes.light
        window.setBackgroundDrawable(ColorDrawable(scheme.canvas.toArgb()))
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        intent.newLinkOrNull()?.let { incomingLink = it }
    }

    /**
     * The link forwarded by [LinkActivity], unless its id was handled before:
     * the system relaunches a task with its original intent, for example from
     * Recents or after an update.
     */
    private fun Intent.newLinkOrNull(): String? {
        val link = getStringExtra(EXTRA_LINK) ?: return null
        val id = getStringExtra(EXTRA_LINK_ID) ?: return null
        val prefs = getSharedPreferences(PREFS, MODE_PRIVATE)
        if (prefs.getString(KEY_HANDLED_LINK_ID, null) == id) return null
        prefs.edit().putString(KEY_HANDLED_LINK_ID, id).apply()
        return link
    }

    companion object {
        const val EXTRA_LINK = "org.supacode.protomobile.app.LINK"
        const val EXTRA_LINK_ID = "org.supacode.protomobile.app.LINK_ID"
        private const val PREFS = "supaproto_ui"
        private const val KEY_HANDLED_LINK_ID = "handled_link_id"
    }
}
