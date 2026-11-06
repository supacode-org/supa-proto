package org.supacode.protomobile.android.ui

import android.Manifest
import android.app.Activity
import android.content.Context
import android.content.pm.PackageManager
import android.net.VpnService
import android.os.Build
import android.widget.Toast
import androidx.activity.compose.PredictiveBackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.ContentTransform
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.SeekableTransitionState
import androidx.compose.animation.core.rememberTransition
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.animation.togetherWith
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.res.stringResource
import org.supacode.protomobile.android.R
import org.supacode.protomobile.shared.AppGraph
import org.supacode.protomobile.shared.ConnectionState
import org.supacode.protomobile.shared.FailureReason
import org.supacode.protomobile.shared.LinkResult
import org.supacode.protomobile.shared.Notice
import org.supacode.protomobile.shared.NoticeLevel
import org.supacode.protomobile.shared.PingState
import org.supacode.protomobile.shared.RoutingMode
import org.supacode.protomobile.shared.ServerLink
import org.supacode.protomobile.shared.ServerLinks
import org.supacode.protomobile.shared.ServerProfile
import kotlinx.coroutines.launch
import kotlin.coroutines.cancellation.CancellationException

private sealed interface Screen {
    data object Home : Screen
    data object Servers : Screen
    data class Editor(val profileId: String?, val imported: ServerLink? = null) : Screen
    data object Scanner : Screen
    data class QrCode(val profile: ServerProfile, val link: String) : Screen
    data object Settings : Screen
    data object Licenses : Screen
}

@Composable
fun JsonProxyUi(graph: AppGraph, incomingLink: String?, onLinkHandled: () -> Unit) {
    val controller = graph.controller
    val context = LocalContext.current
    var stack by remember { mutableStateOf(listOf<Screen>(Screen.Home)) }
    val push: (Screen) -> Unit = { stack = stack + it }
    val pop: () -> Unit = { if (stack.size > 1) stack = stack.dropLast(1) }

    // Page changes animate between stacks; the back gesture seeks the pop animation.
    val navigation = remember { SeekableTransitionState(stack) }
    val scope = rememberCoroutineScope()
    LaunchedEffect(stack) { navigation.animateTo(stack) }
    PredictiveBackHandler(enabled = stack.size > 1) { progress ->
        val previous = stack.dropLast(1)
        try {
            progress.collect { event -> navigation.seekTo(event.progress, previous) }
            stack = previous
        } catch (e: CancellationException) {
            // Gesture cancelled: settle back on the current page.
            scope.launch { navigation.animateTo(stack) }
            throw e
        }
    }

    // Links opened from other apps, including the system camera, always go to a prefilled editor.
    LaunchedEffect(incomingLink) {
        val text = incomingLink ?: return@LaunchedEffect
        onLinkHandled()
        when (val result = ServerLinks.parse(text)) {
            is LinkResult.Success -> push(Screen.Editor(null, result.link))
            is LinkResult.Failure ->
                Toast.makeText(context, context.getString(result.problem.textRes()), Toast.LENGTH_LONG).show()
        }
    }

    LaunchedEffect(controller) {
        controller.notices.collect { notice ->
            val duration = if (notice.level == NoticeLevel.Info) Toast.LENGTH_SHORT else Toast.LENGTH_LONG
            Toast.makeText(context, notice.text(context), duration).show()
        }
    }

    val vpnPermission = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        if (result.resultCode == Activity.RESULT_OK) {
            controller.connect()
        } else {
            Toast.makeText(context, context.getString(R.string.notice_vpn_permission_denied), Toast.LENGTH_LONG).show()
        }
    }
    // The VPN works without it; the permission only shows the status notification.
    val notificationPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {}
    val connect: () -> Unit = {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
            context.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
        val consent = VpnService.prepare(context)
        if (consent != null) vpnPermission.launch(consent) else controller.connect()
    }

    rememberTransition(navigation, label = "navigation").AnimatedContent(
        transitionSpec = { pageTransition(forward = targetState.size >= initialState.size) },
        contentKey = { it.last() },
    ) { shown ->
        Page(shown.last(), graph, connect, push, pop) { stack = it(stack) }
    }
}

/** Shared-axis page motion: pages slide a little and cross-fade, the new page on top. */
private fun pageTransition(forward: Boolean): ContentTransform {
    val direction = if (forward) 1 else -1
    val spec = tween<IntOffset>(PAGE_ANIMATION_MS, easing = FastOutSlowInEasing)
    val fade = tween<Float>(PAGE_ANIMATION_MS, easing = FastOutSlowInEasing)
    return (slideInHorizontally(spec) { direction * it / 5 } + fadeIn(fade))
        .togetherWith(slideOutHorizontally(spec) { -direction * it / 5 } + fadeOut(fade))
        .apply { targetContentZIndex = if (forward) 1f else -1f }
}

private const val PAGE_ANIMATION_MS = 300

@Composable
private fun Page(
    screen: Screen,
    graph: AppGraph,
    connect: () -> Unit,
    push: (Screen) -> Unit,
    pop: () -> Unit,
    replaceStack: ((List<Screen>) -> List<Screen>) -> Unit,
) {
    when (screen) {
        Screen.Home -> HomeScreen(
            graph = graph,
            onConnect = connect,
            onServers = { push(Screen.Servers) },
            onSettings = { push(Screen.Settings) },
        )
        Screen.Servers -> ServerListScreen(
            graph = graph,
            onBack = pop,
            onEdit = { push(Screen.Editor(it)) },
            onScan = { push(Screen.Scanner) },
            onShowQr = { profile, link -> push(Screen.QrCode(profile, link)) },
        )
        is Screen.Editor -> ServerEditorScreen(graph = graph, profileId = screen.profileId, imported = screen.imported, onDone = pop)
        // The editor replaces the scanner so going back returns to the list.
        Screen.Scanner -> QrScannerScreen(onClose = pop, onLink = { link -> replaceStack { it.dropLast(1) + Screen.Editor(null, link) } })
        is Screen.QrCode -> ServerQrScreen(profile = screen.profile, link = screen.link, onBack = pop)
        Screen.Settings -> SettingsScreen(graph = graph, onBack = pop, onLicenses = { push(Screen.Licenses) })
        Screen.Licenses -> LicensesScreen(onBack = pop)
    }
}

@Composable
internal fun ConnectionState.label(): String = when (this) {
    ConnectionState.Disconnected -> stringResource(R.string.state_disconnected)
    is ConnectionState.Connecting -> stringResource(R.string.state_connecting, server)
    is ConnectionState.Connected -> stringResource(R.string.state_connected, server)
    ConnectionState.Disconnecting -> stringResource(R.string.state_disconnecting)
    is ConnectionState.Failed -> when (reason) {
        FailureReason.NoServerSelected -> stringResource(R.string.state_failed_no_server)
        FailureReason.MissingSecret -> stringResource(R.string.state_failed_missing_secret, detail)
        FailureReason.Error -> stringResource(R.string.state_failed_error, detail)
    }
}

internal fun ConnectionState.isActive(): Boolean =
    this is ConnectionState.Connecting || this is ConnectionState.Connected || this == ConnectionState.Disconnecting

/** Toast text; the Go core and shared code only supply details. */
internal fun Notice.text(context: Context): String = when (this) {
    is Notice.Connected -> context.getString(R.string.notice_connected, detail)
    is Notice.ConnectionFailed -> when (reason) {
        FailureReason.NoServerSelected -> context.getString(R.string.state_failed_no_server)
        FailureReason.MissingSecret -> context.getString(R.string.state_failed_missing_secret, detail)
        FailureReason.Error -> context.getString(R.string.notice_connection_failed, detail)
    }
    is Notice.ProxyUnavailable -> context.getString(R.string.notice_proxy_unavailable, detail)
    is Notice.BootstrapFailed -> context.getString(R.string.notice_bootstrap_failed, detail)
    is Notice.DohUnavailable -> context.getString(R.string.notice_doh_unavailable, detail)
}

@Composable
internal fun PingState.label(): String = when (this) {
    PingState.Testing -> stringResource(R.string.ping_testing)
    is PingState.Success ->
        if (millis < 1) stringResource(R.string.ping_under_1ms) else stringResource(R.string.ping_ms, millis)
    is PingState.Failed ->
        if (detail.isEmpty()) stringResource(R.string.ping_invalid_address) else stringResource(R.string.ping_unreachable)
}

@Composable
internal fun RoutingMode.title(): String = stringResource(
    when (this) {
        RoutingMode.Global -> R.string.mode_global
        RoutingMode.GeoIpCn -> R.string.mode_geoip_cn
        RoutingMode.Custom -> R.string.mode_custom
    },
)

@Composable
internal fun RoutingMode.footnote(): String = stringResource(
    when (this) {
        RoutingMode.Global -> R.string.mode_global_footnote
        RoutingMode.GeoIpCn -> R.string.mode_geoip_cn_footnote
        RoutingMode.Custom -> R.string.mode_custom_footnote
    },
)
