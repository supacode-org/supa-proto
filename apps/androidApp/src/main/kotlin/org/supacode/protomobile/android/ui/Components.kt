package org.supacode.protomobile.android.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.withStyle
import org.supacode.protomobile.shared.LatencyGrade
import org.supacode.protomobile.shared.PingState
import org.supacode.terminalui.TerminalColors
import org.supacode.terminalui.TerminalGlyph
import org.supacode.terminalui.TerminalLatencyBar
import org.supacode.terminalui.TerminalRadius
import org.supacode.terminalui.TerminalType

// App-only pieces on top of Terminal UI; everything generic comes from the library.

internal val ControlShape = RoundedCornerShape(TerminalRadius.md)

/** `✕ message` under a group of controls; fields show their own errors. */
@Composable
internal fun FieldError(text: String) {
    Row {
        Glyph(TerminalGlyph.ERROR + " ", TerminalColors.danger, TerminalType.label)
        Text(text, style = TerminalType.label, color = TerminalColors.danger)
    }
}

/** Text color of a latency result. */
@Composable
internal fun LatencyGrade.color(): Color = when (this) {
    LatencyGrade.Testing -> TerminalColors.textSecondary
    LatencyGrade.Good -> TerminalColors.accentText
    LatencyGrade.Slow -> TerminalColors.warning
    LatencyGrade.Failed -> TerminalColors.danger
}

/** Character bar of a ping; decorative, the latency text says the same. Nothing while testing. */
@Composable
internal fun LatencyBar(ping: PingState) {
    Box(Modifier.clearAndSetSemantics {}) {
        TerminalLatencyBar(ms = (ping as? PingState.Success)?.millis, failed = ping is PingState.Failed)
    }
}

/** Splits a formatted rate such as `4.60 MB/s` into number and unit. */
internal fun splitRate(rate: String): Pair<String, String> {
    val space = rate.lastIndexOf(' ')
    return if (space < 0) rate to "" else rate.substring(0, space) to rate.substring(space + 1)
}

/** Plain text that screen readers skip, for glyphs such as `→`. */
@Composable
internal fun Glyph(text: String, color: Color = TerminalColors.textSecondary, style: TextStyle = TerminalType.body) {
    Text(text, style = style, color = color, modifier = Modifier.clearAndSetSemantics {})
}

internal fun AnnotatedString.Builder.appendColored(text: String, color: Color) {
    withStyle(SpanStyle(color = color)) { append(text) }
}
