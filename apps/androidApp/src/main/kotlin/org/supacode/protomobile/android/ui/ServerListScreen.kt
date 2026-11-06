package org.supacode.protomobile.android.ui

import android.widget.Toast
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import org.supacode.protomobile.android.R
import org.supacode.protomobile.shared.AppGraph
import org.supacode.protomobile.shared.Latency
import org.supacode.protomobile.shared.PingState
import org.supacode.protomobile.shared.ServerLinks
import org.supacode.protomobile.shared.ServerProfile
import org.supacode.protomobile.shared.ServerSummary
import org.supacode.terminalui.TerminalButton
import org.supacode.terminalui.TerminalButtonDecoration
import org.supacode.terminalui.TerminalButtonVariant
import org.supacode.terminalui.TerminalCard
import org.supacode.terminalui.TerminalCardDivider
import org.supacode.terminalui.TerminalColors
import org.supacode.terminalui.TerminalGlyph
import org.supacode.terminalui.TerminalIcon
import org.supacode.terminalui.TerminalIconImage
import org.supacode.terminalui.TerminalMenu
import org.supacode.terminalui.TerminalMenuItem
import org.supacode.terminalui.TerminalNote
import org.supacode.terminalui.TerminalPage
import org.supacode.terminalui.TerminalPageHeader
import org.supacode.terminalui.TerminalPathTitle
import org.supacode.terminalui.TerminalSpacing
import org.supacode.terminalui.TerminalType

@Composable
internal fun ServerListScreen(
    graph: AppGraph,
    onBack: () -> Unit,
    onEdit: (String?) -> Unit,
    onScan: () -> Unit,
    onShowQr: (ServerProfile, String) -> Unit,
) {
    val context = LocalContext.current
    val profiles by graph.profiles.profiles.collectAsStateWithLifecycle()
    val selectedId by graph.profiles.selectedId.collectAsStateWithLifecycle()
    val pings by graph.controller.pings.collectAsStateWithLifecycle()

    TerminalPage {
        TerminalPageHeader(
            title = { TerminalPathTitle(listOf(stringResource(R.string.path_servers))) },
            onBack = onBack,
        )
        Row(horizontalArrangement = Arrangement.spacedBy(TerminalSpacing.space8)) {
            TerminalButton(stringResource(R.string.action_scan), onClick = onScan, modifier = Modifier.weight(1f))
            TerminalButton(
                stringResource(R.string.action_test_all),
                onClick = { graph.controller.pingAll() },
                enabled = profiles.isNotEmpty(),
                modifier = Modifier.weight(1f),
            )
            TerminalButton(
                stringResource(R.string.action_add),
                onClick = { onEdit(null) },
                variant = TerminalButtonVariant.Primary,
                decorate = TerminalButtonDecoration.Add,
                modifier = Modifier.weight(1f),
            )
        }
        Summary(ServerSummary.of(profiles, pings))
        if (profiles.isEmpty()) {
            TerminalNote(stringResource(R.string.servers_empty))
        } else {
            TerminalCard {
                profiles.forEachIndexed { index, profile ->
                    if (index > 0) TerminalCardDivider()
                    ServerRow(
                        profile = profile,
                        selected = profile.id == selectedId,
                        ping = pings[profile.server],
                        onSelect = { graph.profiles.select(profile.id) },
                        onEdit = { onEdit(profile.id) },
                        onPing = { graph.controller.ping(profile.id) },
                        onDelete = { graph.profiles.delete(profile.id) },
                        onShowQr = {
                            val secret = graph.profiles.secret(profile.id)
                            if (secret == null) {
                                Toast.makeText(
                                    context, context.getString(R.string.state_failed_missing_secret, profile.name), Toast.LENGTH_LONG,
                                ).show()
                            } else {
                                onShowQr(profile, ServerLinks.format(profile, secret))
                            }
                        },
                    )
                }
            }
            Column(verticalArrangement = Arrangement.spacedBy(TerminalSpacing.space2)) {
                TerminalNote(TerminalGlyph.CURRENT + " " + stringResource(R.string.servers_note_selected))
                TerminalNote(stringResource(R.string.servers_note_latency))
            }
        }
    }
}

/** Total servers, the best latency and how many failed; `—` until there are results. */
@Composable
private fun Summary(summary: ServerSummary) {
    val none = TerminalGlyph.NONE
    TerminalCard {
        Row(Modifier.height(IntrinsicSize.Min)) {
            SummaryCell(stringResource(R.string.summary_total), summary.total.toString(), TerminalColors.textPrimary)
            Box(Modifier.width(1.dp).fillMaxHeight().background(TerminalColors.border))
            SummaryCell(
                stringResource(R.string.summary_best),
                summary.bestMillis?.let {
                    if (it < 1) stringResource(R.string.ping_under_1ms) else stringResource(R.string.ping_ms, it.toInt())
                } ?: none,
                if (summary.bestMillis != null) TerminalColors.accentText else TerminalColors.textSecondary,
            )
            Box(Modifier.width(1.dp).fillMaxHeight().background(TerminalColors.border))
            SummaryCell(
                stringResource(R.string.summary_failed),
                summary.failed?.toString() ?: none,
                if ((summary.failed ?: 0) > 0) TerminalColors.danger else TerminalColors.textSecondary,
            )
        }
    }
}

@Composable
private fun androidx.compose.foundation.layout.RowScope.SummaryCell(label: String, value: String, color: Color) {
    Column(
        Modifier.weight(1f).padding(horizontal = TerminalSpacing.space12, vertical = TerminalSpacing.space10).semantics(mergeDescendants = true) {},
        verticalArrangement = Arrangement.spacedBy(TerminalSpacing.space2),
    ) {
        Text(label, style = TerminalType.caption, color = TerminalColors.textSecondary, maxLines = 1, overflow = TextOverflow.Ellipsis)
        Text(value, style = TerminalType.stat, color = color, maxLines = 1)
    }
}

@Composable
private fun ServerRow(
    profile: ServerProfile,
    selected: Boolean,
    ping: PingState?,
    onSelect: () -> Unit,
    onEdit: () -> Unit,
    onPing: () -> Unit,
    onDelete: () -> Unit,
    onShowQr: () -> Unit,
) {
    var menuOpen by remember { mutableStateOf(false) }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .background(if (selected) TerminalColors.surfaceSelected else Color.Transparent)
            .clickable(onClick = onSelect)
            .semantics { this.selected = selected }
            .padding(start = TerminalSpacing.space14, top = TerminalSpacing.space12, bottom = TerminalSpacing.space12, end = TerminalSpacing.space4),
        horizontalArrangement = Arrangement.spacedBy(TerminalSpacing.space10),
    ) {
        Box(Modifier.width(14.dp)) {
            if (selected) Glyph(TerminalGlyph.CURRENT, TerminalColors.accentText, TerminalType.body.copy(fontWeight = FontWeight.Bold))
        }
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(TerminalSpacing.space4)) {
            Row(horizontalArrangement = Arrangement.spacedBy(TerminalSpacing.space8)) {
                Text(
                    profile.name,
                    style = TerminalType.bodyMedium,
                    color = TerminalColors.textPrimary,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
                if (ping != null) {
                    Text(ping.label(), style = TerminalType.small, color = Latency.grade(ping).color(), maxLines = 1)
                }
            }
            Text(profile.server, style = TerminalType.label, color = TerminalColors.textSecondary, maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (ping != null) LatencyBar(ping)
        }
        // A plain `⋮` keeps the rows quiet; the menu itself is the library's.
        Box {
            val more = stringResource(R.string.action_more)
            IconButton(onClick = { menuOpen = true }, modifier = Modifier.semantics { contentDescription = more }) {
                TerminalIconImage(TerminalIcon.MoreVert, tint = TerminalColors.textSecondary, size = 20.dp)
            }
            TerminalMenu(
                expanded = menuOpen,
                onDismissRequest = { menuOpen = false },
                items = listOf(
                    TerminalMenuItem(stringResource(R.string.action_edit), onSelect = onEdit),
                    TerminalMenuItem(stringResource(R.string.action_test_latency), onSelect = onPing),
                    TerminalMenuItem(stringResource(R.string.action_show_qr), onSelect = onShowQr),
                    TerminalMenuItem(stringResource(R.string.action_delete), danger = true, onSelect = onDelete),
                ),
            )
        }
    }
}
