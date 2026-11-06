package org.supacode.protomobile.android.ui

import android.graphics.Bitmap
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.google.zxing.BarcodeFormat
import com.google.zxing.EncodeHintType
import com.google.zxing.qrcode.QRCodeWriter
import com.google.zxing.qrcode.decoder.ErrorCorrectionLevel
import org.supacode.protomobile.android.R
import org.supacode.protomobile.shared.ServerProfile
import org.supacode.terminalui.TerminalBanner
import org.supacode.terminalui.TerminalBannerKind
import org.supacode.terminalui.TerminalCard
import org.supacode.terminalui.TerminalCardDivider
import org.supacode.terminalui.TerminalColors
import org.supacode.terminalui.TerminalPage
import org.supacode.terminalui.TerminalPageHeader
import org.supacode.terminalui.TerminalPathTitle
import org.supacode.terminalui.TerminalRadius
import org.supacode.terminalui.TerminalSpacing
import org.supacode.terminalui.TerminalType

/** Shows a saved server as a QR code for another device to scan. */
@Composable
internal fun ServerQrScreen(profile: ServerProfile, link: String, onBack: () -> Unit) {
    val qr = remember(link) { qrBitmap(link) }
    TerminalPage(
        footer = { TerminalBanner(stringResource(R.string.qr_warning), kind = TerminalBannerKind.Warning) },
    ) {
        TerminalPageHeader(
            title = { TerminalPathTitle(listOf(stringResource(R.string.path_servers), stringResource(R.string.path_qr))) },
            onBack = onBack,
        )
        TerminalCard {
            Row(Modifier.fillMaxWidth().padding(horizontal = TerminalSpacing.space14, vertical = TerminalSpacing.space10)) {
                Text(
                    stringResource(R.string.qr_file),
                    style = TerminalType.label,
                    color = TerminalColors.textSecondary,
                    modifier = Modifier.weight(1f),
                )
                Text(stringResource(R.string.link_scheme), style = TerminalType.label, color = TerminalColors.textSecondary)
            }
            TerminalCardDivider()
            Column(
                modifier = Modifier.fillMaxWidth().padding(TerminalSpacing.space20),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.spacedBy(TerminalSpacing.space14),
            ) {
                Image(
                    bitmap = qr.asImageBitmap(),
                    contentDescription = stringResource(R.string.qr_content_description, profile.name),
                    // One pixel per module; scale up without smoothing.
                    filterQuality = FilterQuality.None,
                    modifier = Modifier
                        .widthIn(max = 280.dp)
                        .fillMaxWidth()
                        .aspectRatio(1f)
                        // The white card is the code's quiet zone, not a palette color.
                        .background(Color.White, RoundedCornerShape(TerminalRadius.sm))
                        .padding(TerminalSpacing.space16),
                )
                Text(
                    profile.name,
                    style = TerminalType.title.copy(fontSize = TerminalType.bodyMedium.fontSize),
                    fontWeight = FontWeight.Bold,
                    color = TerminalColors.textPrimary,
                )
                Text(profile.server, style = TerminalType.label, color = TerminalColors.textSecondary, textAlign = TextAlign.Center)
            }
        }
    }
}

/** QR code with error correction level M, one pixel per module, no quiet zone (the white card provides it). */
private fun qrBitmap(text: String): Bitmap {
    val matrix = QRCodeWriter().encode(
        text, BarcodeFormat.QR_CODE, 0, 0,
        mapOf(EncodeHintType.ERROR_CORRECTION to ErrorCorrectionLevel.M, EncodeHintType.MARGIN to 0),
    )
    val pixels = IntArray(matrix.width * matrix.height) { i ->
        if (matrix[i % matrix.width, i / matrix.width]) android.graphics.Color.BLACK else android.graphics.Color.WHITE
    }
    return Bitmap.createBitmap(pixels, matrix.width, matrix.height, Bitmap.Config.ARGB_8888)
}
