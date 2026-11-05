package com.json2proxy.android.ui

import android.widget.Toast
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.relocation.BringIntoViewRequester
import androidx.compose.foundation.relocation.bringIntoViewRequester
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.input.KeyboardType
import com.json2proxy.android.R
import com.json2proxy.shared.AppGraph
import com.json2proxy.shared.CoverTraffic
import com.json2proxy.shared.ObfsProviders
import com.json2proxy.shared.PerformanceModes
import com.json2proxy.shared.ProfileProblem
import com.json2proxy.shared.ProfileValidator
import com.json2proxy.shared.ServerLink
import com.json2proxy.shared.ServerProfile
import com.json2proxy.shared.normalizeAscii
import kotlinx.coroutines.launch
import org.supacode.terminalui.TerminalBanner
import org.supacode.terminalui.TerminalButton
import org.supacode.terminalui.TerminalButtonVariant
import org.supacode.terminalui.TerminalChoiceList
import org.supacode.terminalui.TerminalChoiceOption
import org.supacode.terminalui.TerminalIcon
import org.supacode.terminalui.TerminalNote
import org.supacode.terminalui.TerminalPage
import org.supacode.terminalui.TerminalPageHeader
import org.supacode.terminalui.TerminalPathTitle
import org.supacode.terminalui.TerminalSection
import org.supacode.terminalui.TerminalSegmented
import org.supacode.terminalui.TerminalSegmentedOption
import org.supacode.terminalui.TerminalSpacing
import org.supacode.terminalui.TerminalSwitchRow
import org.supacode.terminalui.TerminalTextField

/** Parts of the form that a validation problem points at. */
private enum class Field { Name, Server, ClientId, Secret, Obfs, Participation, Cover, Transport }

private fun ProfileProblem.field(): Field = when (this) {
    ProfileProblem.NameRequired -> Field.Name
    ProfileProblem.InvalidServer -> Field.Server
    ProfileProblem.ClientIdRequired -> Field.ClientId
    ProfileProblem.SecretNotBase64Url, ProfileProblem.SecretTooShort -> Field.Secret
    ProfileProblem.UnknownObfsProvider -> Field.Obfs
    ProfileProblem.InvalidParticipation -> Field.Participation
    ProfileProblem.UnknownPerformanceMode -> Field.Transport
    ProfileProblem.InvalidCoverRate -> Field.Cover
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
internal fun ServerEditorScreen(graph: AppGraph, profileId: String?, imported: ServerLink?, onDone: () -> Unit) {
    val context = LocalContext.current
    val existing = remember(profileId) { graph.profiles.profiles.value.firstOrNull { it.id == profileId } }
    // An imported link prefills a new server; the user reviews it before saving.
    val initial = existing ?: imported?.profile
    var name by remember { mutableStateOf(initial?.name.orEmpty()) }
    var server by remember { mutableStateOf(initial?.server.orEmpty()) }
    var clientId by remember { mutableStateOf(initial?.clientId.orEmpty()) }
    var secret by remember { mutableStateOf(imported?.secret.orEmpty()) }
    var obfs by remember { mutableStateOf(initial?.obfsProvider ?: ObfsProviders.NONE) }
    var participation by remember { mutableStateOf((initial?.obfsParticipation ?: 0.0).toString()) }
    var performance by remember { mutableStateOf(initial?.performanceMode ?: PerformanceModes.INTERACTIVE) }
    var udp by remember { mutableStateOf(initial?.udpEnabled ?: true) }
    var cover by remember { mutableStateOf(initial?.coverEnabled ?: false) }
    var coverRate by remember { mutableStateOf((initial?.coverRateKbps ?: CoverTraffic.DEFAULT_RATE_KBPS).toString()) }
    var problems by remember { mutableStateOf(emptyList<ProfileProblem>()) }

    val scope = rememberCoroutineScope()
    // Saving with problems brings the first field in error into view.
    val requesters = remember { Field.entries.associateWith { BringIntoViewRequester() } }
    fun Modifier.tracked(field: Field) = bringIntoViewRequester(requesters.getValue(field))

    val save: () -> Unit = {
        val profile = ServerProfile(
            id = existing?.id ?: graph.profiles.generateId(),
            name = name.trim(),
            server = normalizeAscii(server),
            clientId = normalizeAscii(clientId),
            obfsProvider = obfs,
            obfsParticipation = normalizeAscii(participation).toDoubleOrNull() ?: Double.NaN,
            performanceMode = performance,
            udpEnabled = udp,
            // A provider without a cover profile cannot carry cover traffic.
            coverEnabled = cover && ObfsProviders.supportsCover(obfs),
            coverRateKbps = normalizeAscii(coverRate).toIntOrNull() ?: -1,
        )
        // Editing with an empty field keeps the stored secret.
        val newSecret = if (existing != null && secret.isEmpty()) null else normalizeAscii(secret)
        problems = ProfileValidator.validate(profile, newSecret)
        if (problems.isEmpty()) {
            if (graph.profiles.save(profile, newSecret)) {
                onDone()
            } else {
                Toast.makeText(context, context.getString(R.string.secret_store_failed), Toast.LENGTH_LONG).show()
            }
        } else {
            val first = problems.map { it.field() }.minOrNull()
            if (first != null) scope.launch { requesters.getValue(first).bringIntoView() }
        }
    }

    @Composable
    fun errorFor(field: Field): String? = problems.firstOrNull { it.field() == field }?.text()

    TerminalPage {
        TerminalPageHeader(
            title = {
                TerminalPathTitle(
                    listOf(
                        stringResource(R.string.path_servers),
                        stringResource(if (existing == null) R.string.path_new else R.string.path_edit),
                    ),
                )
            },
            onBack = onDone,
            backIcon = if (existing == null) TerminalIcon.Close else TerminalIcon.ArrowBack,
            actions = { TerminalButton(stringResource(R.string.action_save), onClick = save, variant = TerminalButtonVariant.Primary) },
        )
        if (imported != null) TerminalBanner(stringResource(R.string.import_review))

        Column(verticalArrangement = Arrangement.spacedBy(TerminalSpacing.space14)) {
            TerminalTextField(
                value = name, onValueChange = { name = it },
                label = stringResource(R.string.field_name),
                error = errorFor(Field.Name),
                keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Words),
                modifier = Modifier.tracked(Field.Name),
            )
            TerminalTextField(
                value = server, onValueChange = { server = it },
                label = stringResource(R.string.field_server),
                error = errorFor(Field.Server),
                keyboardOptions = TechnicalKeyboard.copy(keyboardType = KeyboardType.Uri),
                modifier = Modifier.tracked(Field.Server),
            )
            TerminalTextField(
                value = clientId, onValueChange = { clientId = it },
                label = stringResource(R.string.field_client_id),
                error = errorFor(Field.ClientId),
                keyboardOptions = TechnicalKeyboard,
                modifier = Modifier.tracked(Field.ClientId),
            )
            TerminalTextField(
                value = secret, onValueChange = { secret = it },
                label = stringResource(if (existing == null) R.string.field_secret else R.string.field_secret_keep),
                error = errorFor(Field.Secret),
                password = true,
                keyboardOptions = TechnicalKeyboard,
                modifier = Modifier.tracked(Field.Secret),
            )
        }

        TerminalSection(stringResource(R.string.section_obfuscation), Modifier.tracked(Field.Obfs)) {
            TerminalSegmented(ObfsProviders.all.map { TerminalSegmentedOption(it, obfsLabel(it)) }, obfs, onSelect = {
                obfs = it
                participation = if (it == ObfsProviders.NONE) "0" else ObfsProviders.DEFAULT_PARTICIPATION.toString()
            })
            errorFor(Field.Obfs)?.let { FieldError(it) }
            // Participation only matters once a provider is chosen.
            if (obfs != ObfsProviders.NONE || errorFor(Field.Participation) != null) {
                TerminalTextField(
                    value = participation, onValueChange = { participation = it },
                    label = stringResource(R.string.field_participation),
                    error = errorFor(Field.Participation),
                    keyboardOptions = TechnicalKeyboard.copy(keyboardType = KeyboardType.Decimal),
                    modifier = Modifier.tracked(Field.Participation),
                )
            }
        }

        TerminalSection(stringResource(R.string.section_cover), Modifier.tracked(Field.Cover)) {
            val supported = ObfsProviders.supportsCover(obfs)
            TerminalSwitchRow(stringResource(R.string.field_cover), cover && supported, onCheckedChange = { cover = it }, enabled = supported)
            if (!supported) {
                TerminalNote(stringResource(R.string.cover_needs_provider))
            } else if (cover) {
                TerminalTextField(
                    value = coverRate, onValueChange = { coverRate = it },
                    label = stringResource(R.string.field_cover_rate),
                    error = errorFor(Field.Cover),
                    keyboardOptions = TechnicalKeyboard.copy(keyboardType = KeyboardType.Number),
                )
                val rate = normalizeAscii(coverRate).toIntOrNull()
                if (rate != null && rate in CoverTraffic.MIN_RATE_KBPS..CoverTraffic.MAX_RATE_KBPS) {
                    TerminalNote(stringResource(R.string.cover_estimate, CoverTraffic.monthlyEstimate(rate)))
                }
            }
        }

        TerminalSection(stringResource(R.string.section_transport), Modifier.tracked(Field.Transport)) {
            TerminalChoiceList(
                PerformanceModes.all.map { TerminalChoiceOption(it, performanceLabel(it), detail = it) },
                performance,
                onSelect = { performance = it },
            )
            errorFor(Field.Transport)?.let { FieldError(it) }
            TerminalSwitchRow(stringResource(R.string.field_udp), udp, onCheckedChange = { udp = it })
        }
    }
}

/** Addresses, IDs and secrets: no capitalization or autocorrect, ASCII keyboard. */
internal val TechnicalKeyboard = KeyboardOptions(
    capitalization = KeyboardCapitalization.None,
    autoCorrectEnabled = false,
    keyboardType = KeyboardType.Ascii,
)

/** Provider names are configuration values; only "none" is translated. */
@Composable
private fun obfsLabel(provider: String): String =
    if (provider == ObfsProviders.NONE) stringResource(R.string.obfs_none) else provider

@Composable
private fun performanceLabel(mode: String): String = when (mode) {
    PerformanceModes.INTERACTIVE -> stringResource(R.string.perf_interactive)
    PerformanceModes.THROUGHPUT -> stringResource(R.string.perf_throughput)
    else -> mode
}

@Composable
private fun ProfileProblem.text(): String = when (this) {
    ProfileProblem.NameRequired -> stringResource(R.string.problem_name_required)
    ProfileProblem.InvalidServer -> stringResource(R.string.problem_invalid_server)
    ProfileProblem.ClientIdRequired -> stringResource(R.string.problem_client_id_required)
    ProfileProblem.SecretNotBase64Url -> stringResource(R.string.problem_secret_not_base64url)
    ProfileProblem.SecretTooShort -> stringResource(R.string.problem_secret_too_short)
    ProfileProblem.UnknownObfsProvider -> stringResource(R.string.problem_unknown_obfs)
    ProfileProblem.InvalidParticipation -> stringResource(R.string.problem_invalid_participation)
    ProfileProblem.UnknownPerformanceMode -> stringResource(R.string.problem_unknown_performance)
    ProfileProblem.InvalidCoverRate ->
        stringResource(R.string.problem_invalid_cover_rate, CoverTraffic.MIN_RATE_KBPS, CoverTraffic.MAX_RATE_KBPS)
}
