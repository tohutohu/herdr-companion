package com.tohutohu.herdrcompanion.ui.files

import androidx.compose.animation.core.animate
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.gestures.calculateCentroid
import androidx.compose.foundation.gestures.calculatePan
import androidx.compose.foundation.gestures.calculateZoom
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.input.pointer.positionChanged
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.unit.toSize
import coil3.compose.AsyncImage
import kotlinx.coroutines.launch
import kotlin.math.abs
import kotlin.math.max
import kotlin.math.min

/**
 * An image fitted into its bounds that zooms with a pinch around the fingers,
 * pans while zoomed without leaving the picture's edges, and toggles zoom on a
 * double tap. Unzoomed, one-finger drags are left for whatever is underneath.
 */
@Composable
fun ZoomableImage(model: Any?, contentDescription: String?, modifier: Modifier = Modifier) {
    var zoom by remember(model) { mutableStateOf(ImageZoom()) }
    var bounds by remember { mutableStateOf(Size.Zero) }
    var intrinsic by remember(model) { mutableStateOf(Size.Unspecified) }
    val scope = rememberCoroutineScope()
    Box(
        modifier
            .onSizeChanged { bounds = it.toSize() }
            .pointerInput(model) {
                detectTapGestures(onDoubleTap = { tap ->
                    val from = zoom
                    val to = if (from.scale > 1.01f) ImageZoom() else from.zoomed(DOUBLE_TAP_SCALE / from.scale, tap, bounds, fittedSize(intrinsic, bounds))
                    scope.launch {
                        animate(0f, 1f) { t, _ -> zoom = from.lerp(to, t) }
                    }
                })
            }
            .pointerInput(model) {
                awaitEachGesture {
                    awaitFirstDown(requireUnconsumed = false)
                    do {
                        val event = awaitPointerEvent()
                        if (event.changes.any { it.isConsumed }) continue
                        val pressed = event.changes.count { it.pressed }
                        val factor = event.calculateZoom()
                        val pan = event.calculatePan()
                        // A one-finger drag on an unzoomed image isn't ours.
                        if (pressed < 2 && zoom.scale <= 1f) continue
                        if (factor == 1f && pan == Offset.Zero) continue
                        val content = fittedSize(intrinsic, bounds)
                        zoom = zoom.zoomed(factor, event.calculateCentroid(useCurrent = true), bounds, content).panned(pan, bounds, content)
                        event.changes.forEach { if (it.positionChanged()) it.consume() }
                    } while (event.changes.any { it.pressed })
                }
            },
    ) {
        AsyncImage(
            model = model,
            contentDescription = contentDescription,
            onSuccess = { intrinsic = it.painter.intrinsicSize },
            modifier = Modifier
                .fillMaxSize()
                .graphicsLayer(
                    scaleX = zoom.scale,
                    scaleY = zoom.scale,
                    translationX = zoom.offset.x,
                    translationY = zoom.offset.y,
                ),
        )
    }
}

private const val DOUBLE_TAP_SCALE = 2.5f

/**
 * Scale and translation of a picture drawn centred in [bounds], with the scale
 * applied around the centre of the bounds (the graphicsLayer default).
 */
internal data class ImageZoom(val scale: Float = 1f, val offset: Offset = Offset.Zero) {
    /** Multiplies the scale by [factor], keeping the point under [centroid] still. */
    fun zoomed(factor: Float, centroid: Offset, bounds: Size, content: Size): ImageZoom {
        val next = (scale * factor).coerceIn(MIN_SCALE, MAX_SCALE)
        val fromCentre = centroid - Offset(bounds.width / 2, bounds.height / 2)
        val moved = fromCentre - (fromCentre - offset) * (next / scale)
        return ImageZoom(next, moved).clamped(bounds, content)
    }

    fun panned(pan: Offset, bounds: Size, content: Size): ImageZoom =
        copy(offset = offset + pan).clamped(bounds, content)

    /** Keeps the picture's edges from coming inside the bounds while it overflows them. */
    fun clamped(bounds: Size, content: Size): ImageZoom {
        if (scale <= 1f) return ImageZoom()
        val maxX = max(0f, (content.width * scale - bounds.width) / 2)
        val maxY = max(0f, (content.height * scale - bounds.height) / 2)
        return copy(offset = Offset(offset.x.coerceIn(-maxX, maxX), offset.y.coerceIn(-maxY, maxY)))
    }

    fun lerp(to: ImageZoom, t: Float): ImageZoom = ImageZoom(
        scale + (to.scale - scale) * t,
        offset + (to.offset - offset) * t,
    )

    companion object {
        const val MIN_SCALE = 1f
        const val MAX_SCALE = 8f
    }
}

/** The size a picture of [intrinsic] size takes when fitted into [bounds]; the bounds while it's unknown. */
internal fun fittedSize(intrinsic: Size, bounds: Size): Size {
    if (intrinsic == Size.Unspecified || intrinsic.width <= 0f || intrinsic.height <= 0f) return bounds
    if (abs(bounds.width) < 1f || abs(bounds.height) < 1f) return bounds
    val fit = min(bounds.width / intrinsic.width, bounds.height / intrinsic.height)
    return Size(intrinsic.width * fit, intrinsic.height * fit)
}
