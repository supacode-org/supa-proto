package org.supacode.protomobile.android.ui

import android.content.Intent
import android.provider.Settings
import android.widget.Toast
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.KeyboardType
import org.supacode.protomobile.android.R
import org.supacode.protomobile.shared.AppGraph
import org.supacode.protomobile.shared.AppSettings
import org.supacode.protomobile.shared.ModeOptions
import org.supacode.protomobile.shared.RoutingMode
import org.supacode.protomobile.shared.TrafficUnit
import org.supacode.protomobile.shared.normalizeAscii
import org.supacode.terminalui.TerminalButton
import org.supacode.terminalui.TerminalButtonVariant
import org.supacode.terminalui.TerminalChoiceList
import org.supacode.terminalui.TerminalChoiceOption
import org.supacode.terminalui.TerminalGlyph
import org.supacode.terminalui.TerminalNote
import org.supacode.terminalui.TerminalPage
import org.supacode.terminalui.TerminalPageHeader
import org.supacode.terminalui.TerminalPathTitle
import org.supacode.terminalui.TerminalSection
import org.supacode.terminalui.TerminalSwitchRow
import org.supacode.terminalui.TerminalTextField

@Composable
internal fun SettingsScreen(graph: AppGraph, onBack: () -> Unit, onLicenses: () -> Unit) {
    val context = LocalContext.current
    val initial = remember { graph.settings.current }
    var mode by remember { mutableStateOf(initial.routingMode) }
    var rules by remember { mutableStateOf(initial.customRules) }
    // Each routing mode keeps its own DNS options; switching modes only changes which ones are shown.
    var modeOptions by remember { mutableStateOf(RoutingMode.entries.associateWith { initial.options(it) }) }
    val options = modeOptions.getValue(mode)
    val editOptions: (ModeOptions) -> Unit = { modeOptions = modeOptions + (mode to it) }
    var killSwitch by remember { mutableStateOf(initial.killSwitch) }
    var trafficUnit by remember { mutableStateOf(initial.trafficUnit) }
    var rulesError by remember { mutableStateOf<String?>(null) }

    val save: () -> Unit = {
        rulesError = if (mode == RoutingMode.Custom) graph.controller.validateRules(rules) else null
        if (rulesError == null) {
            graph.settings.update(
                initial.copy(
                    routingMode = mode,
                    customRules = rules,
                    modeOptions = modeOptions.entries.associate { (key, value) ->
                        key.wire to ModeOptions(normalizeAscii(value.dohUrl), normalizeAscii(value.bootstrap))
                    },
                    killSwitch = killSwitch,
                    trafficUnit = trafficUnit,
                ),
            )
            Toast.makeText(context, context.getString(R.string.settings_saved), Toast.LENGTH_SHORT).show()
            onBack()
        }
    }

    TerminalPage(
        footer = {
            // A small, quiet entry at the very end of the page.
            TerminalButton(
                stringResource(R.string.settings_licenses),
                onClick = onLicenses,
                modifier = Modifier.align(Alignment.CenterHorizontally),
            )
        },
    ) {
        TerminalPageHeader(
            title = { TerminalPathTitle(listOf(stringResource(R.string.path_settings))) },
            onBack = onBack,
            actions = { TerminalButton(stringResource(R.string.action_save), onClick = save, variant = TerminalButtonVariant.Primary) },
        )
        TerminalSection(stringResource(R.string.section_routing)) {
            TerminalChoiceList(
                RoutingMode.entries.map { TerminalChoiceOption(it, it.title(), detail = it.wire) },
                mode,
                onSelect = { mode = it },
            )
            if (mode == RoutingMode.Custom) {
                TerminalTextField(
                    value = rules,
                    onValueChange = { rules = it },
                    label = stringResource(R.string.field_rules),
                    error = rulesError?.let { stringResource(R.string.rules_invalid, it) },
                    keyboardOptions = TechnicalKeyboard,
                    multiline = true,
                    rows = 6,
                )
            }
            TerminalNote(mode.footnote())
        }

        TerminalSection(stringResource(R.string.section_dns)) {
            TerminalTextField(
                value = options.dohUrl,
                onValueChange = { editOptions(options.copy(dohUrl = it)) },
                label = stringResource(R.string.field_doh),
                placeholder = mode.defaultDoH,
                keyboardOptions = TechnicalKeyboard.copy(keyboardType = KeyboardType.Uri),
            )
            TerminalTextField(
                value = options.bootstrap,
                onValueChange = { editOptions(options.copy(bootstrap = it)) },
                label = stringResource(R.string.field_bootstrap),
                placeholder = AppSettings.DEFAULT_BOOTSTRAP,
                keyboardOptions = TechnicalKeyboard,
            )
            TerminalNote(stringResource(R.string.dns_footnote))
        }

        TerminalSection(stringResource(R.string.section_protection)) {
            TerminalSwitchRow(stringResource(R.string.field_kill_switch), killSwitch, onCheckedChange = { killSwitch = it })
            TerminalNote(stringResource(R.string.kill_switch_footnote))
            TerminalButton(
                stringResource(R.string.action_system_vpn_settings) + " " + TerminalGlyph.EXTERNAL,
                onClick = { context.startActivity(Intent(Settings.ACTION_VPN_SETTINGS)) },
                fullWidth = true,
            )
            TerminalNote(stringResource(R.string.always_on_footnote))
        }

        TerminalSection(stringResource(R.string.section_traffic_unit)) {
            TerminalChoiceList(TrafficUnit.entries.map { TerminalChoiceOption(it, it.title()) }, trafficUnit, onSelect = { trafficUnit = it })
        }
    }
}

@Composable
private fun TrafficUnit.title(): String = stringResource(
    when (this) {
        TrafficUnit.Bytes -> R.string.traffic_unit_bytes
        TrafficUnit.Bits -> R.string.traffic_unit_bits
    },
)
