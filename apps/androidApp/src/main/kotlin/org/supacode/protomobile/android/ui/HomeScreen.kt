package org.supacode.protomobile.android.ui

import android.os.Build
import androidx.compose.foundation.ScrollState
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.WindowInsetsSides
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.navigationBars
import androidx.compose.foundation.layout.only
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.systemBars
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import org.supacode.protomobile.android.R
import org.supacode.protomobile.shared.AppGraph
import org.supacode.protomobile.shared.ConnectionState
import org.supacode.protomobile.shared.CoverStats
import org.supacode.protomobile.shared.Latency
import org.supacode.protomobile.shared.RoutingMode
import org.supacode.protomobile.shared.TrafficFormat
import org.supacode.protomobile.shared.TrafficPoint
import org.supacode.protomobile.shared.TrafficStats
import org.supacode.protomobile.shared.TrafficUnit
import org.supacode.terminalui.TerminalBanner
import org.supacode.terminalui.TerminalBannerKind
import org.supacode.terminalui.TerminalButton
import org.supacode.terminalui.TerminalButtonDecoration
import org.supacode.terminalui.TerminalButtonSize
import org.supacode.terminalui.TerminalButtonVariant
import org.supacode.terminalui.TerminalCard
import org.supacode.terminalui.TerminalCardDivider
import org.supacode.terminalui.TerminalChartLegend
import org.supacode.terminalui.TerminalColors
import org.supacode.terminalui.TerminalGlyph
import org.supacode.terminalui.TerminalIcon
import org.supacode.terminalui.TerminalIconButton
import org.supacode.terminalui.TerminalLegendItem
import org.supacode.terminalui.TerminalNote
import org.supacode.terminalui.TerminalPageHeader
import org.supacode.terminalui.TerminalPromptTitle
import org.supacode.terminalui.TerminalSize
import org.supacode.terminalui.TerminalSpacing
import org.supacode.terminalui.TerminalTrafficChart
import org.supacode.terminalui.TerminalTrafficPoint
import org.supacode.terminalui.TerminalType

@Composable
internal fun HomeScreen(
    graph: AppGraph,
    onConnect: () -> Unit,
    onServers: () -> Unit,
    onSettings: () -> Unit,
) {
    val state by graph.controller.state.collectAsStateWithLifecycle()
    val traffic by graph.controller.traffic.collectAsStateWithLifecycle()
    val history by graph.controller.trafficHistory.collectAsStateWithLifecycle()
    val profiles by graph.profiles.profiles.collectAsStateWithLifecycle()
    val selectedId by graph.profiles.selectedId.collectAsStateWithLifecycle()
    val settings by graph.settings.settings.collectAsStateWithLifecycle()
    val pings by graph.controller.pings.collectAsStateWithLifecycle()
    val selected = profiles.firstOrNull { it.id == selectedId }

    HomePage(
        bottom = {
            TerminalCard {
                NavRow(
                    label = stringResource(R.string.home_server),
                    title = selected?.name ?: stringResource(R.string.home_add_server),
                    subtitle = selected?.server,
                    trailing = selected?.let { pings[it.server] }?.let { ping -> ping.label() to Latency.grade(ping).color() },
                    onClick = onServers,
                )
                TerminalCardDivider()
                NavRow(
                    label = stringResource(R.string.section_routing),
                    title = settings.routingMode.title(),
                    onClick = onSettings,
                )
                TerminalCardDivider()
                NavRow(
                    label = stringResource(R.string.field_kill_switch),
                    title = null,
                    trailing = if (settings.killSwitch) {
                        stringResource(R.string.kill_switch_on) to TerminalColors.accentText
                    } else {
                        stringResource(R.string.kill_switch_off) to TerminalColors.textSecondary
                    },
                    onClick = onSettings,
                )
            }
        },
        footer = { SystemFooter() },
    ) {
        TerminalPageHeader(
            title = { TerminalPromptTitle(stringResource(R.string.glyph_command)) },
            actions = { TerminalIconButton(TerminalIcon.Tune, stringResource(R.string.home_settings), onClick = onSettings) },
        )
        SessionCard(
            state = state,
            server = selected?.name,
            routing = settings.routingMode,
            traffic = traffic,
            history = history,
            unit = settings.trafficUnit,
        )
        if (state is ConnectionState.Failed) TerminalBanner(state.label(), kind = TerminalBannerKind.Danger, icon = null)
        ConnectButton(state, enabled = selected != null, onConnect = onConnect, onDisconnect = { graph.controller.disconnect() })
    }
}

/**
 * The home page: [org.supacode.terminalui.TerminalPage]'s layout plus a [footer] line that is
 * not part of the page. It follows the screen-high content, so it only shows once scrolled to:
 * unlike the iOS home indicator area, the navigation bar inset has no room for it.
 */
@Composable
private fun HomePage(
    bottom: @Composable ColumnScope.() -> Unit,
    footer: @Composable () -> Unit,
    scroll: ScrollState = rememberScrollState(),
    content: @Composable ColumnScope.() -> Unit,
) {
    val density = LocalDensity.current
    val navigationInset = with(density) { WindowInsets.navigationBars.getBottom(density).toDp() }
    BoxWithConstraints(
        Modifier
            .fillMaxSize()
            .background(TerminalColors.canvas)
            .windowInsetsPadding(WindowInsets.systemBars.only(WindowInsetsSides.Top + WindowInsetsSides.Horizontal)),
    ) {
        val viewport = maxHeight - navigationInset
        Column(Modifier.fillMaxSize().verticalScroll(scroll)) {
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .heightIn(min = viewport)
                    .padding(horizontal = TerminalSpacing.space20, vertical = TerminalSpacing.space16),
                verticalArrangement = Arrangement.SpaceBetween,
            ) {
                Column(verticalArrangement = Arrangement.spacedBy(TerminalSpacing.space20), content = content)
                Column(
                    modifier = Modifier.fillMaxWidth().padding(top = TerminalSpacing.space20),
                    verticalArrangement = Arrangement.spacedBy(TerminalSpacing.space12),
                    content = bottom,
                )
            }
            Box(
                Modifier.fillMaxWidth().padding(start = TerminalSpacing.space20, end = TerminalSpacing.space20, bottom = TerminalSpacing.space8),
                contentAlignment = Alignment.Center,
            ) { footer() }
            Spacer(Modifier.height(navigationInset))
        }
    }
}

/** Android version, device model and app version, under the page. */
@Composable
private fun SystemFooter() {
    val context = LocalContext.current
    val line = remember {
        val version = context.packageManager.getPackageInfo(context.packageName, 0).versionName.orEmpty()
        listOf("Android ${Build.VERSION.RELEASE}", Build.MODEL, "v$version").joinToString(" · ")
    }
    Text(line, style = TerminalType.caption, color = TerminalColors.textSecondary, maxLines = 1, overflow = TextOverflow.Ellipsis)
}

/** `session.json`: the connection as a small JSON object, then live traffic. */
@Composable
private fun SessionCard(
    state: ConnectionState,
    server: String?,
    routing: RoutingMode,
    traffic: TrafficStats?,
    history: List<TrafficPoint>,
    unit: TrafficUnit,
) {
    val live = state is ConnectionState.Connected
    val (stateValue, stateColor) = when (state) {
        is ConnectionState.Connected -> stringResource(R.string.json_state_connected) to TerminalColors.accentText
        is ConnectionState.Connecting -> stringResource(R.string.json_state_connecting) to TerminalColors.warning
        ConnectionState.Disconnecting -> stringResource(R.string.json_state_disconnecting) to TerminalColors.warning
        is ConnectionState.Failed -> stringResource(R.string.json_state_failed) to TerminalColors.danger
        ConnectionState.Disconnected -> stringResource(R.string.json_state_disconnected) to TerminalColors.textSecondary
    }
    val description = stringResource(R.string.home_session_description, state.label(), routing.title())
    val keyState = stringResource(R.string.json_key_state)
    val keyServer = stringResource(R.string.json_key_server)
    val keyRouting = stringResource(R.string.json_key_routing)
    val nullValue = stringResource(R.string.json_null)

    TerminalCard {
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = TerminalSpacing.space14, vertical = TerminalSpacing.space10),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                stringResource(R.string.session_file),
                style = TerminalType.label,
                color = TerminalColors.textSecondary,
                modifier = Modifier.weight(1f),
            )
            Box(Modifier.size(7.dp).background(if (live) TerminalColors.accent else TerminalColors.textSecondary, CircleShape))
            Text(
                stringResource(if (live) R.string.session_live else R.string.session_idle),
                style = TerminalType.label,
                color = if (live) TerminalColors.accentText else TerminalColors.textSecondary,
                modifier = Modifier.padding(start = TerminalSpacing.space6),
            )
        }
        TerminalCardDivider()
        val punctuation = TerminalColors.textSecondary
        val keyColor = TerminalColors.syntaxKey
        val stringColor = TerminalColors.syntaxString
        val numberColor = TerminalColors.syntaxNumber
        val json = buildAnnotatedString {
            fun key(name: String) {
                append("  ")
                appendColored("\"$name\"", keyColor)
                appendColored(": ", punctuation)
            }
            appendColored("{\n", punctuation)
            key(keyState)
            appendColored("\"$stateValue\"", stateColor)
            appendColored(",\n", punctuation)
            key(keyServer)
            if (server != null) appendColored("\"$server\"", stringColor) else appendColored(nullValue, numberColor)
            appendColored(",\n", punctuation)
            key(keyRouting)
            appendColored("\"${routing.wire}\"", stringColor)
            appendColored("\n}", punctuation)
        }
        Text(
            json,
            style = TerminalType.code,
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = TerminalSpacing.space14, vertical = TerminalSpacing.space12)
                .clearAndSetSemantics { contentDescription = description },
        )
        TerminalCardDivider()
        if (live && traffic != null) {
            TrafficPanel(traffic, history, unit)
        } else {
            TerminalNote(stringResource(R.string.traffic_waiting), Modifier.padding(TerminalSpacing.space14))
        }
    }
}

@Composable
private fun TrafficPanel(traffic: TrafficStats, history: List<TrafficPoint>, unit: TrafficUnit) {
    val download = TrafficFormat.rate(traffic.downloadSpeed, unit)
    val upload = TrafficFormat.rate(traffic.uploadSpeed, unit)
    Column(
        Modifier.padding(start = TerminalSpacing.space14, end = TerminalSpacing.space14, top = TerminalSpacing.space12, bottom = TerminalSpacing.space14),
        verticalArrangement = Arrangement.spacedBy(TerminalSpacing.space10),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            TerminalNote(stringResource(R.string.traffic_legend), Modifier.weight(1f))
            TerminalChartLegend(
                listOf(
                    TerminalLegendItem(stringResource(R.string.traffic_download), TerminalColors.syntaxKey),
                    TerminalLegendItem(stringResource(R.string.traffic_upload), TerminalColors.syntaxNumber),
                ),
            )
        }
        TerminalTrafficChart(
            history.map { TerminalTrafficPoint(up = it.uploadSpeed.toDouble(), down = it.downloadSpeed.toDouble()) },
            stringResource(R.string.traffic_chart_description, download, upload),
        )
        Row(horizontalArrangement = Arrangement.spacedBy(TerminalSpacing.space8)) {
            StatCell(TerminalGlyph.DOWNLOAD, stringResource(R.string.traffic_download), download, TerminalColors.syntaxKey, Modifier.weight(1f))
            StatCell(TerminalGlyph.UPLOAD, stringResource(R.string.traffic_upload), upload, TerminalColors.syntaxNumber, Modifier.weight(1f))
        }
        Row(horizontalArrangement = Arrangement.spacedBy(TerminalSpacing.space8)) {
            StatCell(null, stringResource(R.string.stat_tcp), traffic.tcpConnections.toString(), TerminalColors.textPrimary, Modifier.weight(1f))
            StatCell(null, stringResource(R.string.stat_udp), traffic.udpConnections.toString(), TerminalColors.textPrimary, Modifier.weight(1f))
        }
        if (traffic.cover.isActive) CoverLine(traffic.cover, unit, Modifier.align(Alignment.End))
        TotalLine(traffic, unit, Modifier.align(Alignment.End))
    }
}

/** Cover connections and speeds, kept apart from the real traffic above. */
@Composable
private fun CoverLine(cover: CoverStats, unit: TrafficUnit, modifier: Modifier) {
    val download = TrafficFormat.rate(cover.downloadSpeed, unit)
    val upload = TrafficFormat.rate(cover.uploadSpeed, unit)
    val description = stringResource(R.string.traffic_cover_description, cover.connections, download, upload)
    val label = stringResource(R.string.traffic_cover)
    val connections = stringResource(R.string.traffic_cover_connections, cover.connections)
    val secondary = TerminalColors.textSecondary
    val primary = TerminalColors.textPrimary
    Text(
        buildAnnotatedString {
            appendColored("$label ", secondary)
            appendColored(connections, primary)
            appendColored(" ${TerminalGlyph.DOT} ", secondary)
            appendColored("${TerminalGlyph.DOWNLOAD} ", TerminalColors.syntaxKey)
            appendColored(download, primary)
            appendColored(" ${TerminalGlyph.DOT} ", secondary)
            appendColored("${TerminalGlyph.UPLOAD} ", TerminalColors.syntaxNumber)
            appendColored(upload, primary)
        },
        style = TerminalType.caption,
        maxLines = 1,
        modifier = modifier.clearAndSetSemantics { contentDescription = description },
    )
}

/** Upload and download of every connection so far, at the panel's bottom right. */
@Composable
private fun TotalLine(traffic: TrafficStats, unit: TrafficUnit, modifier: Modifier) {
    val download = TrafficFormat.amount(traffic.cumulativeDownload, unit)
    val upload = TrafficFormat.amount(traffic.cumulativeUpload, unit)
    val description = stringResource(R.string.traffic_total_description, download, upload)
    val label = stringResource(R.string.traffic_total)
    val secondary = TerminalColors.textSecondary
    val primary = TerminalColors.textPrimary
    val downColor = TerminalColors.syntaxKey
    val upColor = TerminalColors.syntaxNumber
    Text(
        buildAnnotatedString {
            appendColored("$label ", secondary)
            appendColored("${TerminalGlyph.DOWNLOAD} ", downColor)
            appendColored(download, primary)
            appendColored(" ${TerminalGlyph.DOT} ", secondary)
            appendColored("${TerminalGlyph.UPLOAD} ", upColor)
            appendColored(upload, primary)
        },
        style = TerminalType.caption,
        maxLines = 1,
        modifier = modifier.clearAndSetSemantics { contentDescription = description },
    )
}

/** Label over a value; a rate's unit is smaller and dimmed. */
@Composable
private fun StatCell(glyph: String?, label: String, value: String, color: Color, modifier: Modifier) {
    val (number, unit) = splitRate(value)
    Column(
        modifier = modifier
            .border(1.dp, TerminalColors.border, ControlShape)
            .padding(horizontal = TerminalSpacing.space10, vertical = TerminalSpacing.space8),
        verticalArrangement = Arrangement.spacedBy(TerminalSpacing.space2),
    ) {
        Row {
            if (glyph != null) Glyph("$glyph ", style = TerminalType.caption)
            Text(label, style = TerminalType.caption, color = TerminalColors.textSecondary)
        }
        val unitColor = TerminalColors.textSecondary
        Text(
            buildAnnotatedString {
                appendColored(number, color)
                if (unit.isNotEmpty()) appendColored(" $unit", unitColor)
            },
            style = TerminalType.stat,
            maxLines = 1,
        )
    }
}

@Composable
private fun ConnectButton(state: ConnectionState, enabled: Boolean, onConnect: () -> Unit, onDisconnect: () -> Unit) {
    // The decorated labels read badly aloud; screen readers get the plain action.
    val connect = stringResource(R.string.action_connect)
    val disconnect = stringResource(R.string.action_disconnect)
    if (state.isActive()) {
        // The small text of the outlined actions, at the large button's height.
        TerminalButton(
            text = disconnect,
            onClick = onDisconnect,
            variant = TerminalButtonVariant.Danger,
            decorate = TerminalButtonDecoration.Bracket,
            fullWidth = true,
            enabled = state != ConnectionState.Disconnecting,
            modifier = Modifier.heightIn(min = TerminalSize.row).semantics { contentDescription = disconnect },
        )
    } else {
        TerminalButton(
            text = connect,
            onClick = onConnect,
            variant = TerminalButtonVariant.Primary,
            size = TerminalButtonSize.Lg,
            decorate = TerminalButtonDecoration.Prompt,
            fullWidth = true,
            enabled = enabled,
            modifier = Modifier.semantics { contentDescription = connect },
        )
    }
}

/** A row of the home card: a short label, a value, and an optional colored status. */
@Composable
private fun NavRow(
    label: String,
    title: String?,
    subtitle: String? = null,
    trailing: Pair<String, Color>? = null,
    onClick: () -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .heightIn(min = 52.dp)
            .clickable(onClick = onClick)
            .padding(horizontal = TerminalSpacing.space14, vertical = TerminalSpacing.space10),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(TerminalSpacing.space12),
    ) {
        Text(label, style = TerminalType.small, color = TerminalColors.textSecondary, modifier = Modifier.width(96.dp))
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(TerminalSpacing.space2)) {
            if (title != null) {
                Text(title, style = TerminalType.bodyMedium, color = TerminalColors.textPrimary, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            if (subtitle != null) {
                Text(subtitle, style = TerminalType.label, color = TerminalColors.textSecondary, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        }
        trailing?.let { (text, color) ->
            Text(text, style = TerminalType.small.copy(fontWeight = FontWeight.Medium), color = color, maxLines = 1)
        }
        Glyph(TerminalGlyph.NEXT)
    }
}
