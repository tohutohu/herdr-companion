package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.animation.core.animate
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.snap
import androidx.compose.animation.core.tween
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.nestedscroll.NestedScrollConnection
import androidx.compose.ui.input.nestedscroll.NestedScrollSource
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.Velocity
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch

/** The finger travels twice as far as the indicator. */
private const val DRAG_RATE = 0.5f
private val THRESHOLD = 64.dp
private val INDICATOR_SIZE = 40.dp
private val INDICATOR_MARGIN = 16.dp

internal const val BOTTOM_PULL_INDICATOR_TAG = "bottomPullIndicator"

/**
 * Pulling the bottom-up list past its newest message fetches the
 * conversation now, as pulling a timeline down past its top does. Only drags
 * pull: a fling that reaches the end stops there, so reading back down to the
 * newest message never refreshes by accident.
 *
 * Nested-scroll deltas are in screen direction even for a reversed list, so a
 * pull is the upward drag the list has left over at its end.
 */
@Stable
internal class BottomPullState(
    private val scope: CoroutineScope,
    private val thresholdPx: Float,
    private val onRefresh: () -> Unit,
) {
    /** How far the indicator has been pulled up, in pixels. */
    var distance by mutableFloatStateOf(0f)
        private set
    private var settle: Job? = null

    val progress get() = distance / thresholdPx

    val connection = object : NestedScrollConnection {
        override fun onPreScroll(available: Offset, source: NestedScrollSource): Offset {
            // Dragging back down first takes back the pull, then scrolls.
            if (source != NestedScrollSource.UserInput || distance <= 0f || available.y <= 0f) return Offset.Zero
            val used = minOf(available.y, distance / DRAG_RATE)
            pullBy(-used * DRAG_RATE)
            return Offset(0f, used)
        }

        override fun onPostScroll(consumed: Offset, available: Offset, source: NestedScrollSource): Offset {
            if (source != NestedScrollSource.UserInput || available.y >= 0f) return Offset.Zero
            pullBy(-available.y * DRAG_RATE)
            return Offset(0f, available.y)
        }

        override suspend fun onPreFling(available: Velocity): Velocity {
            if (distance <= 0f) return Velocity.Zero
            release()
            return available
        }
    }

    private fun pullBy(delta: Float) {
        settle?.cancel()
        distance = (distance + delta).coerceIn(0f, thresholdPx * 2)
    }

    /** Lets go: past the threshold it refreshes. The indicator always returns. */
    fun release() {
        if (distance <= 0f) return
        if (distance >= thresholdPx) onRefresh()
        settle?.cancel()
        settle = scope.launch { animate(distance, 0f, animationSpec = tween(200)) { v, _ -> distance = v } }
    }
}

@Composable
internal fun rememberBottomPullState(onRefresh: () -> Unit): BottomPullState {
    val scope = rememberCoroutineScope()
    val thresholdPx = with(LocalDensity.current) { THRESHOLD.toPx() }
    val refresh by rememberUpdatedState(onRefresh)
    return remember(scope, thresholdPx) { BottomPullState(scope, thresholdPx) { refresh() } }
}

/**
 * Rises from the bottom edge with the pull, drawing in its circle as the pull
 * nears the threshold, and spins at the threshold height while [refreshing].
 */
@Composable
internal fun BoxScope.BottomPullIndicator(state: BottomPullState, refreshing: Boolean) {
    val density = LocalDensity.current
    val thresholdPx = with(density) { THRESHOLD.toPx() }
    val hiddenPx = with(density) { (INDICATOR_SIZE + INDICATOR_MARGIN).toPx() }
    // Taken over at once from the released pull, so it does not dip first.
    val hold by animateFloatAsState(
        if (refreshing) thresholdPx else 0f,
        animationSpec = if (refreshing) snap() else tween(200),
        label = "pullHold",
    )
    val lift = maxOf(state.distance, hold)
    if (lift <= 0f) return
    Box(
        Modifier
            .align(Alignment.BottomCenter)
            .padding(bottom = INDICATOR_MARGIN)
            .graphicsLayer {
                translationY = hiddenPx - lift
                alpha = (lift / (thresholdPx * 0.5f)).coerceAtMost(1f)
            }
            .testTag(BOTTOM_PULL_INDICATOR_TAG),
    ) {
        Surface(
            shape = CircleShape,
            color = MaterialTheme.colorScheme.surfaceContainerHigh,
            shadowElevation = 3.dp,
            modifier = Modifier.size(INDICATOR_SIZE),
        ) {
            Box(contentAlignment = Alignment.Center) {
                val indicator = Modifier.size(22.dp)
                if (refreshing) {
                    CircularProgressIndicator(indicator, strokeWidth = 2.5.dp)
                } else {
                    CircularProgressIndicator(
                        progress = { state.progress.coerceAtMost(1f) },
                        modifier = indicator,
                        strokeWidth = 2.5.dp,
                    )
                }
            }
        }
    }
}
