package org.supacode.protomobile.android.ui

import android.Manifest
import android.app.Activity
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.content.res.Configuration
import android.net.Uri
import android.os.Build
import android.provider.Settings
import android.util.Size
import android.view.HapticFeedbackConstants
import android.view.View
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.camera.core.Camera
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.ImageProxy
import androidx.camera.core.Preview
import androidx.camera.core.resolutionselector.ResolutionSelector
import androidx.camera.core.resolutionselector.ResolutionStrategy
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.VectorConverter
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.spring
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.RoundRect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.PathFillType
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.core.view.WindowCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.lifecycle.compose.LocalLifecycleOwner
import com.google.zxing.BarcodeFormat
import com.google.zxing.BinaryBitmap
import com.google.zxing.DecodeHintType
import com.google.zxing.MultiFormatReader
import com.google.zxing.PlanarYUVLuminanceSource
import com.google.zxing.ReaderException
import com.google.zxing.common.HybridBinarizer
import org.supacode.protomobile.android.R
import org.supacode.protomobile.shared.LinkProblem
import org.supacode.protomobile.shared.LinkResult
import org.supacode.protomobile.shared.ServerLink
import org.supacode.protomobile.shared.ServerLinks
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import java.util.concurrent.Executors
import kotlin.math.max
import kotlin.math.min
import org.supacode.terminalui.TerminalBanner
import org.supacode.terminalui.TerminalBannerKind
import org.supacode.terminalui.TerminalCard
import org.supacode.terminalui.TerminalColors
import org.supacode.terminalui.TerminalGlyph
import org.supacode.terminalui.TerminalIcon
import org.supacode.terminalui.TerminalIconButton
import org.supacode.terminalui.TerminalOpacity
import org.supacode.terminalui.TerminalSpacing
import org.supacode.terminalui.TerminalTheme
import org.supacode.terminalui.TerminalTone
import org.supacode.terminalui.TerminalType

private object ScannerLayout {
    val viewfinderMaxSide = 260.dp
    const val VIEWFINDER_WIDTH_RATIO = 0.68f
    val viewfinderCenterOffset = (-30).dp
    val viewfinderCornerRadius = 32.dp
    val bracketLength = 26.dp
    val bracketLineWidth = 5.dp
    val codeHighlightPadding = 10.dp
    val buttonSide = 44.dp
    val hintHeight = 44.dp
    val hintBottomInset = 24.dp
    const val SUCCESS_DISMISS_MILLIS = 350L
    const val HINT_RESET_MILLIS = 2000L

    /** Decode a little beyond the viewfinder so codes touching its edge still read. */
    const val CROP_MARGIN = 1.15f
}

/**
 * Full-screen camera scanner for server QR codes, styled after the iOS one:
 * dimmed preview with a rounded viewfinder, breathing corner brackets that snap
 * onto an accepted code, close and flashlight buttons, and a hint card that
 * briefly shows why a code was rejected. Always dark, as it sits over the camera.
 */
@Composable
internal fun QrScannerScreen(onClose: () -> Unit, onLink: (ServerLink) -> Unit) {
    LightSystemBarIcons()
    TerminalTheme(darkTheme = true) { Scanner(onClose, onLink) }
}

/**
 * Light status and navigation bar icons while shown, whatever the system appearance; again
 * after an appearance switch, which resets them. Leaving restores the icons of the appearance.
 */
@Composable
private fun LightSystemBarIcons() {
    val view = LocalView.current
    val uiMode = LocalConfiguration.current.uiMode
    DisposableEffect(view, uiMode) {
        val window = (view.context as? Activity)?.window ?: return@DisposableEffect onDispose {}
        val controller = WindowCompat.getInsetsController(window, view)
        controller.isAppearanceLightStatusBars = false
        controller.isAppearanceLightNavigationBars = false
        onDispose {
            val night = view.resources.configuration.uiMode and Configuration.UI_MODE_NIGHT_MASK == Configuration.UI_MODE_NIGHT_YES
            controller.isAppearanceLightStatusBars = !night
            controller.isAppearanceLightNavigationBars = !night
        }
    }
}

@Composable
private fun Scanner(onClose: () -> Unit, onLink: (ServerLink) -> Unit) {
    val context = LocalContext.current
    val view = LocalView.current
    val scope = rememberCoroutineScope()

    var granted by remember { mutableStateOf(context.hasCameraPermission()) }
    var permissionDenied by remember { mutableStateOf(false) }
    var cameraFailed by remember { mutableStateOf(false) }
    val permission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {
        granted = it
        permissionDenied = !it
    }
    LaunchedEffect(Unit) { if (!granted) permission.launch(Manifest.permission.CAMERA) }
    // Returning from system settings.
    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) {
        if (!granted && context.hasCameraPermission()) {
            granted = true
            permissionDenied = false
        }
    }
    val geometry = remember { ScanGeometry() }
    val bracket = remember { Animatable(Rect.Zero, Rect.VectorConverter) }
    var handled by remember { mutableStateOf(false) }
    var rejection by remember { mutableStateOf<String?>(null) }
    var lastRejected by remember { mutableStateOf<String?>(null) }
    var resetJob by remember { mutableStateOf<Job?>(null) }
    var camera by remember { mutableStateOf<Camera?>(null) }
    var torchOn by remember { mutableStateOf(false) }

    val onDetected: (Detection) -> Unit = handle@{ detection ->
        if (handled) return@handle
        when (val result = ServerLinks.parse(detection.text)) {
            is LinkResult.Success -> {
                handled = true
                view.confirmFeedback()
                scope.launch {
                    detection.bounds?.let { code ->
                        val padding = with(view.resources.displayMetrics) { ScannerLayout.codeHighlightPadding.value * density }
                        bracket.animateTo(code.inflate(padding), spring(dampingRatio = 0.7f, stiffness = Spring.StiffnessMediumLow))
                    }
                }
                scope.launch {
                    delay(ScannerLayout.SUCCESS_DISMISS_MILLIS)
                    onLink(result.link)
                }
            }
            is LinkResult.Failure -> {
                // The same invalid code stays in view and decodes every frame; only react once per payload.
                if (detection.text == lastRejected) return@handle
                lastRejected = detection.text
                view.rejectFeedback()
                rejection = context.getString(result.problem.textRes())
                resetJob?.cancel()
                resetJob = scope.launch {
                    delay(ScannerLayout.HINT_RESET_MILLIS)
                    rejection = null
                    lastRejected = null
                }
            }
        }
    }

    BoxWithConstraints(Modifier.fillMaxSize().background(TerminalColors.canvas)) {
        val density = LocalDensity.current
        val viewfinder = with(density) {
            val side = min(maxWidth.toPx() * ScannerLayout.VIEWFINDER_WIDTH_RATIO, ScannerLayout.viewfinderMaxSide.toPx())
            val center = Offset(maxWidth.toPx() / 2, maxHeight.toPx() / 2 + ScannerLayout.viewfinderCenterOffset.toPx())
            Rect(center, side / 2)
        }
        geometry.viewWidth = constraints.maxWidth
        geometry.viewHeight = constraints.maxHeight
        geometry.viewfinder = viewfinder
        LaunchedEffect(viewfinder) { if (!handled) bracket.snapTo(viewfinder) }

        if (granted) {
            CameraPreview(
                geometry = geometry,
                active = !handled,
                onCamera = { camera = it },
                onFailure = { cameraFailed = true },
                onDetected = onDetected,
            )
        }

        val breathing by rememberInfiniteTransition(label = "brackets").animateFloat(
            initialValue = 1f,
            targetValue = 0.45f,
            animationSpec = infiniteRepeatable(tween(1100), RepeatMode.Reverse),
            label = "bracketAlpha",
        )
        val bracketColor = if (rejection != null) TerminalColors.danger else TerminalColors.accent
        Canvas(Modifier.fillMaxSize()) {
            val radius = ScannerLayout.viewfinderCornerRadius.toPx()
            val dim = Path().apply {
                fillType = PathFillType.EvenOdd
                addRect(Rect(Offset.Zero, size))
                addRoundRect(RoundRect(viewfinder, CornerRadius(radius)))
            }
            // Dims the camera picture itself, so it stays black rather than a palette color.
            drawPath(dim, Color.Black.copy(alpha = TerminalOpacity.cameraDim))
            drawPath(
                bracketPath(bracket.value, radius, ScannerLayout.bracketLength.toPx()),
                bracketColor.copy(alpha = if (handled) 1f else breathing),
                style = Stroke(ScannerLayout.bracketLineWidth.toPx(), cap = StrokeCap.Round, join = StrokeJoin.Round),
            )
        }

        Row(
            modifier = Modifier.fillMaxWidth().statusBarsPadding().padding(horizontal = TerminalSpacing.space16, vertical = TerminalSpacing.space8),
            horizontalArrangement = Arrangement.SpaceBetween,
        ) {
            TerminalIconButton(TerminalIcon.Close, stringResource(R.string.action_close), onClick = onClose)
            if (camera?.cameraInfo?.hasFlashUnit() == true) {
                TerminalIconButton(
                    if (torchOn) TerminalIcon.FlashlightOn else TerminalIcon.FlashlightOff,
                    stringResource(R.string.action_flashlight),
                    tone = if (torchOn) TerminalTone.Warning else null,
                    onClick = {
                        torchOn = !torchOn
                        camera?.cameraControl?.enableTorch(torchOn)
                    },
                )
            }
        }

        Box(
            modifier = Modifier
                .align(Alignment.BottomCenter)
                .navigationBarsPadding()
                .padding(start = TerminalSpacing.space20, end = TerminalSpacing.space20, bottom = ScannerLayout.hintBottomInset)
                .heightIn(min = ScannerLayout.hintHeight),
        ) {
            val reason = rejection
            if (reason != null) {
                TerminalBanner(reason, kind = TerminalBannerKind.Danger)
            } else {
                TerminalCard {
                    Row(Modifier.padding(TerminalSpacing.space14), verticalAlignment = Alignment.CenterVertically) {
                        Glyph(TerminalGlyph.CURRENT + " ", TerminalColors.accentText, TerminalType.body.copy(fontWeight = FontWeight.Bold))
                        Text(stringResource(R.string.scanner_hint), style = TerminalType.small, color = TerminalColors.textPrimary)
                    }
                }
            }
        }
    }

    if (permissionDenied) {
        AlertDialog(
            onDismissRequest = onClose,
            text = { Text(stringResource(R.string.camera_permission_disabled)) },
            confirmButton = {
                TextButton(onClick = {
                    context.startActivity(
                        Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.fromParts("package", context.packageName, null)),
                    )
                }) { Text(stringResource(R.string.action_settings)) }
            },
            dismissButton = { TextButton(onClick = onClose) { Text(stringResource(R.string.action_cancel)) } },
        )
    }
    if (cameraFailed) {
        AlertDialog(
            onDismissRequest = onClose,
            text = { Text(stringResource(R.string.camera_unavailable)) },
            confirmButton = { TextButton(onClick = onClose) { Text(stringResource(R.string.action_close)) } },
        )
    }
}

/** A decoded payload and, when known, the code's bounds in view pixels. */
private class Detection(val text: String, val bounds: Rect?)

/** View size and viewfinder, read by the analysis thread to crop and map frames. */
private class ScanGeometry {
    @Volatile var viewWidth = 0
    @Volatile var viewHeight = 0
    @Volatile var viewfinder = Rect.Zero
}

@Composable
private fun CameraPreview(
    geometry: ScanGeometry,
    active: Boolean,
    onCamera: (Camera) -> Unit,
    onFailure: () -> Unit,
    onDetected: (Detection) -> Unit,
) {
    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val previewView = remember { PreviewView(context) }
    val analyzer = remember { QrAnalyzer(geometry) }
    analyzer.active = active
    analyzer.onDetected = onDetected

    DisposableEffect(lifecycleOwner) {
        val executor = Executors.newSingleThreadExecutor()
        val mainExecutor = ContextCompat.getMainExecutor(context)
        analyzer.deliver = { detection -> mainExecutor.execute { analyzer.onDetected(detection) } }
        val future = ProcessCameraProvider.getInstance(context)
        future.addListener({
            try {
                val provider = future.get()
                val preview = Preview.Builder().build().also { it.surfaceProvider = previewView.surfaceProvider }
                val analysis = ImageAnalysis.Builder()
                    .setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST)
                    .setResolutionSelector(
                        ResolutionSelector.Builder()
                            .setResolutionStrategy(
                                ResolutionStrategy(Size(1280, 720), ResolutionStrategy.FALLBACK_RULE_CLOSEST_HIGHER_THEN_LOWER),
                            )
                            .build(),
                    )
                    .build()
                    .also { it.setAnalyzer(executor, analyzer) }
                provider.unbindAll()
                onCamera(provider.bindToLifecycle(lifecycleOwner, CameraSelector.DEFAULT_BACK_CAMERA, preview, analysis))
            } catch (e: Exception) {
                onFailure()
            }
        }, mainExecutor)
        onDispose {
            if (future.isDone) runCatching { future.get().unbindAll() }
            executor.shutdown()
        }
    }
    AndroidView(factory = { previewView }, modifier = Modifier.fillMaxSize())
}

/**
 * Decodes QR codes from the luminance (Y) plane with ZXing, limited to the part
 * of the frame under the viewfinder so codes outside it are ignored.
 */
private class QrAnalyzer(private val geometry: ScanGeometry) : ImageAnalysis.Analyzer {
    @Volatile var active = true
    @Volatile var onDetected: (Detection) -> Unit = {}
    @Volatile var deliver: (Detection) -> Unit = {}
    private val reader = MultiFormatReader().apply {
        setHints(mapOf(DecodeHintType.POSSIBLE_FORMATS to listOf(BarcodeFormat.QR_CODE)))
    }
    private var buffer = ByteArray(0)

    override fun analyze(image: ImageProxy) {
        image.use {
            if (!active) return
            val mapping = FrameMapping.of(image, geometry) ?: return
            val crop = mapping.sensorRect(geometry.viewfinder, ScannerLayout.CROP_MARGIN)
            val plane = image.planes[0]
            val data = plane.buffer
            val size = plane.rowStride * image.height
            if (buffer.size != size) buffer = ByteArray(size)
            data.rewind()
            data.get(buffer, 0, min(size, data.remaining()))
            val source = PlanarYUVLuminanceSource(
                buffer, plane.rowStride, image.height,
                crop.left, crop.top, crop.width(), crop.height(), false,
            )
            val result = try {
                reader.decodeWithState(BinaryBitmap(HybridBinarizer(source)))
            } catch (_: ReaderException) {
                null
            } finally {
                reader.reset()
            } ?: return
            val points = result.resultPoints.orEmpty().filterNotNull()
                .map { mapping.toView(crop.left + it.x, crop.top + it.y) }
            val bounds = if (points.size < 3) null else Rect(
                points.minOf { it.x }, points.minOf { it.y }, points.maxOf { it.x }, points.maxOf { it.y },
            ).let { centers ->
                // Result points are finder pattern centers, 3.5 modules inside the code's corners.
                val grow = max(centers.width, centers.height) * 0.15f
                centers.inflate(grow)
            }
            deliver(Detection(result.text, bounds))
        }
    }
}

/**
 * Maps between sensor-oriented frame pixels and view pixels for a PreviewView
 * in its default FILL_CENTER mode.
 */
private class FrameMapping(
    private val width: Int,
    private val height: Int,
    private val rotation: Int,
    private val scale: Float,
    private val dx: Float,
    private val dy: Float,
) {
    fun toView(sx: Float, sy: Float): Offset {
        val (ux, uy) = when (rotation) {
            90 -> height - sy to sx
            180 -> width - sx to height - sy
            270 -> sy to width - sx
            else -> sx to sy
        }
        return Offset(ux * scale - dx, uy * scale - dy)
    }

    private fun toSensor(vx: Float, vy: Float): Offset {
        val ux = (vx + dx) / scale
        val uy = (vy + dy) / scale
        return when (rotation) {
            90 -> Offset(uy, height - ux)
            180 -> Offset(width - ux, height - uy)
            270 -> Offset(width - uy, ux)
            else -> Offset(ux, uy)
        }
    }

    fun sensorRect(viewRect: Rect, margin: Float): android.graphics.Rect {
        val grown = Rect(viewRect.center, max(viewRect.width, viewRect.height) * margin / 2)
        val a = toSensor(grown.left, grown.top)
        val b = toSensor(grown.right, grown.bottom)
        return android.graphics.Rect(
            min(a.x, b.x).toInt().coerceIn(0, width - 1),
            min(a.y, b.y).toInt().coerceIn(0, height - 1),
            max(a.x, b.x).toInt().coerceIn(1, width),
            max(a.y, b.y).toInt().coerceIn(1, height),
        )
    }

    companion object {
        fun of(image: ImageProxy, geometry: ScanGeometry): FrameMapping? {
            val viewWidth = geometry.viewWidth
            val viewHeight = geometry.viewHeight
            if (viewWidth == 0 || viewHeight == 0 || geometry.viewfinder.isEmpty) return null
            val rotation = image.imageInfo.rotationDegrees
            val uprightWidth = if (rotation % 180 == 0) image.width else image.height
            val uprightHeight = if (rotation % 180 == 0) image.height else image.width
            val scale = max(viewWidth.toFloat() / uprightWidth, viewHeight.toFloat() / uprightHeight)
            return FrameMapping(
                width = image.width,
                height = image.height,
                rotation = rotation,
                scale = scale,
                dx = (uprightWidth * scale - viewWidth) / 2,
                dy = (uprightHeight * scale - viewHeight) / 2,
            )
        }
    }
}

/** Four rounded corner brackets hugging [rect], like the iOS scanner. */
private fun bracketPath(rect: Rect, cornerRadius: Float, bracketLength: Float): Path = Path().apply {
    if (rect.isEmpty) return@apply
    val r = minOf(cornerRadius, rect.width / 4, rect.height / 4)
    val len = max(0f, minOf(bracketLength, rect.width / 2 - r, rect.height / 2 - r))
    val (l, t, rt, b) = listOf(rect.left, rect.top, rect.right, rect.bottom)
    moveTo(l, t + r + len)
    lineTo(l, t + r)
    arcTo(Rect(l, t, l + 2 * r, t + 2 * r), 180f, 90f, false)
    lineTo(l + r + len, t)
    moveTo(rt - r - len, t)
    lineTo(rt - r, t)
    arcTo(Rect(rt - 2 * r, t, rt, t + 2 * r), 270f, 90f, false)
    lineTo(rt, t + r + len)
    moveTo(rt, b - r - len)
    lineTo(rt, b - r)
    arcTo(Rect(rt - 2 * r, b - 2 * r, rt, b), 0f, 90f, false)
    lineTo(rt - r - len, b)
    moveTo(l + r + len, b)
    lineTo(l + r, b)
    arcTo(Rect(l, b - 2 * r, l + 2 * r, b), 90f, 90f, false)
    lineTo(l, b - r - len)
}

private fun Context.hasCameraPermission() =
    checkSelfPermission(Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED

private fun View.confirmFeedback() {
    performHapticFeedback(
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) HapticFeedbackConstants.CONFIRM else HapticFeedbackConstants.LONG_PRESS,
    )
}

private fun View.rejectFeedback() {
    performHapticFeedback(
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) HapticFeedbackConstants.REJECT else HapticFeedbackConstants.LONG_PRESS,
    )
}

internal fun LinkProblem.textRes(): Int = when (this) {
    LinkProblem.NotServerLink -> R.string.link_not_server
    LinkProblem.UnsupportedVersion -> R.string.link_unsupported_version
    LinkProblem.MissingCredentials -> R.string.link_missing_credentials
    LinkProblem.InvalidServer -> R.string.link_invalid_server
    LinkProblem.InvalidParameter -> R.string.link_invalid_parameter
}
