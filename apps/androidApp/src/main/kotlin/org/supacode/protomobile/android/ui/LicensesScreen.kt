package org.supacode.protomobile.android.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import org.supacode.protomobile.android.R
import org.supacode.protomobile.shared.NoticeCategory
import org.supacode.protomobile.shared.NoticeId
import org.supacode.protomobile.shared.NoticeLicense
import org.supacode.protomobile.shared.NoticePlatform
import org.supacode.protomobile.shared.OpenSourceNotice
import org.supacode.protomobile.shared.OpenSourceNotices
import org.supacode.terminalui.TerminalCard
import org.supacode.terminalui.TerminalCardDivider
import org.supacode.terminalui.TerminalColors
import org.supacode.terminalui.TerminalNote
import org.supacode.terminalui.TerminalPage
import org.supacode.terminalui.TerminalPageHeader
import org.supacode.terminalui.TerminalPathTitle
import org.supacode.terminalui.TerminalSection
import org.supacode.terminalui.TerminalSpacing
import org.supacode.terminalui.TerminalType

/** Third-party components and data the app uses, grouped by kind, each with its license. */
@Composable
internal fun LicensesScreen(onBack: () -> Unit) {
    val notices = OpenSourceNotices.forPlatform(NoticePlatform.Android)
    TerminalPage {
        TerminalPageHeader(
            title = { TerminalPathTitle(listOf(stringResource(R.string.path_settings), stringResource(R.string.path_licenses))) },
            onBack = onBack,
        )
        TerminalNote(stringResource(R.string.licenses_intro))
        NoticeCategory.entries.forEach { category ->
            val group = notices.filter { it.category == category }
            if (group.isNotEmpty()) {
                TerminalSection(category.title()) {
                    TerminalCard {
                        group.forEachIndexed { index, notice ->
                            if (index > 0) TerminalCardDivider()
                            NoticeRow(notice)
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun NoticeRow(notice: OpenSourceNotice) {
    var expanded by rememberSaveable(notice.id) { mutableStateOf(false) }
    val uriHandler = LocalUriHandler.current
    Column(Modifier.fillMaxWidth().padding(TerminalSpacing.space14), verticalArrangement = Arrangement.spacedBy(TerminalSpacing.space4)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(notice.name, style = TerminalType.bodyMedium, color = TerminalColors.textPrimary, modifier = Modifier.weight(1f))
            Text(notice.license.title(), style = TerminalType.label, color = TerminalColors.accentText)
        }
        Text(notice.id.purpose(), style = TerminalType.small, color = TerminalColors.textSecondary)
        Text(notice.copyright, style = TerminalType.label, color = TerminalColors.textSecondary)
        Text(
            notice.url,
            style = TerminalType.label,
            color = TerminalColors.syntaxKey,
            modifier = Modifier
                .heightIn(min = 32.dp)
                .clickable(role = Role.Button) { uriHandler.openUri(notice.url) }
                .padding(vertical = 6.dp),
        )
        val text = notice.text
        if (text != null) {
            Text(
                stringResource(if (expanded) R.string.notice_hide_text else R.string.notice_show_text),
                style = TerminalType.small.copy(fontWeight = FontWeight.Medium),
                color = TerminalColors.textPrimary,
                modifier = Modifier
                    .heightIn(min = 44.dp)
                    .clickable(role = Role.Button) { expanded = !expanded }
                    .padding(vertical = 12.dp),
            )
            if (expanded) {
                Text(
                    text,
                    style = TerminalType.caption.copy(fontSize = 10.sp, lineHeight = 14.sp),
                    color = TerminalColors.textSecondary,
                    modifier = Modifier
                        .fillMaxWidth()
                        .background(TerminalColors.canvas, ControlShape)
                        .padding(TerminalSpacing.space12),
                )
            }
        }
    }
}

@Composable
private fun NoticeCategory.title(): String = stringResource(
    when (this) {
        NoticeCategory.Library -> R.string.notice_category_library
        NoticeCategory.Font -> R.string.notice_category_font
        NoticeCategory.Data -> R.string.notice_category_data
    },
)

@Composable
private fun NoticeLicense.title(): String = spdx ?: stringResource(R.string.notice_license_public_data)

@Composable
private fun NoticeId.purpose(): String = stringResource(
    when (this) {
        NoticeId.GoRuntime -> R.string.notice_purpose_go_runtime
        NoticeId.GoExtensions -> R.string.notice_purpose_go_extensions
        NoticeId.Gvisor -> R.string.notice_purpose_gvisor
        NoticeId.Btree -> R.string.notice_purpose_btree
        NoticeId.KotlinStdlib -> R.string.notice_purpose_kotlin
        NoticeId.KotlinxCoroutines -> R.string.notice_purpose_coroutines
        NoticeId.KotlinxSerialization -> R.string.notice_purpose_serialization
        NoticeId.Skie -> R.string.notice_purpose_skie
        NoticeId.AndroidX -> R.string.notice_purpose_androidx
        NoticeId.Zxing -> R.string.notice_purpose_zxing
        NoticeId.TerminalUi -> R.string.notice_purpose_terminal_ui
        NoticeId.JetBrainsMono -> R.string.notice_purpose_jetbrains_mono
        NoticeId.ApnicStats -> R.string.notice_purpose_apnic
    },
)
