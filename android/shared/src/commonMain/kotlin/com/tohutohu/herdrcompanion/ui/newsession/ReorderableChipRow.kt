package com.tohutohu.herdrcompanion.ui.newsession

import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.FiniteAnimationSpec
import androidx.compose.foundation.gestures.detectDragGesturesAfterLongPress
import androidx.compose.foundation.gestures.scrollBy
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.lazy.LazyItemScope
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import androidx.compose.ui.zIndex
import com.tohutohu.herdrcompanion.ui.UiMotion
import com.tohutohu.herdrcompanion.ui.itemPlacementSpec
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch

/**
 * A horizontal row whose [rowItems] can be reordered by long-pressing and
 * dragging. Content added by [beforeItems] and [afterItems] stays fixed and
 * is not part of the reorderable range.
 *
 * The row animates the items' placement with [placementSpec]; [itemContent]
 * must not add its own `animateItem`, because the chip under the finger has to
 * stay there instead of animating to its new slot.
 */
@Composable
fun <T> ReorderableChipRow(
    rowItems: List<T>,
    itemKey: (T) -> Any,
    onMove: (List<T>) -> Unit,
    modifier: Modifier = Modifier,
    contentPadding: PaddingValues = PaddingValues(),
    horizontalArrangement: Arrangement.Horizontal = Arrangement.Start,
    placementSpec: FiniteAnimationSpec<IntOffset>? = itemPlacementSpec(paused = false),
    beforeItems: LazyListScope.() -> Unit = {},
    afterItems: LazyListScope.() -> Unit = {},
    itemContent: @Composable LazyItemScope.(T, Modifier) -> Unit,
) {
    val listState = rememberLazyListState()
    val scope = rememberCoroutineScope()
    var orderedItems by remember { mutableStateOf(rowItems) }
    // The chip being dragged, or settling into its slot after the drop.
    var activeKey by remember { mutableStateOf<Any?>(null) }
    var dragging by remember { mutableStateOf(false) }
    var dragOrigin by remember { mutableStateOf<List<T>?>(null) }
    // Center of the lifted chip, in the row's viewport.
    var dragCenter by remember { mutableFloatStateOf(0f) }
    var liftCenter by remember { mutableFloatStateOf(0f) }
    // 0 while lifted; 1 once the chip has settled into its slot.
    val release = remember { Animatable(0f) }
    var settleJob by remember { mutableStateOf<Job?>(null) }
    val haptic = LocalHapticFeedback.current
    val density = LocalDensity.current

    LaunchedEffect(rowItems) {
        if (!dragging) orderedItems = rowItems
    }

    fun endDrag(commit: Boolean) {
        val origin = dragOrigin
        val result = orderedItems
        if (!commit && origin != null) orderedItems = origin
        dragging = false
        dragOrigin = null
        if (commit && origin != null && origin != result) onMove(result)
        settleJob = scope.launch {
            release.animateTo(1f, UiMotion.resize())
            activeKey = null
        }
    }

    // Moves the lifted chip [key] to the slot under the finger.
    fun moveUnderFinger(key: Any) {
        val fromIndex = orderedItems.indexOfFirst { itemKey(it) == key }
        if (fromIndex < 0) return

        val reorderableKeys = orderedItems.mapTo(mutableSetOf()) { itemKey(it) }
        val candidate = listState.layoutInfo.visibleItemsInfo
            .filter { it.key in reorderableKeys && it.key != key }
            .sortedBy { it.offset }
            .firstOrNull { dragCenter < it.offset + it.size / 2f }
        val insertionIndex = candidate?.let { target ->
            orderedItems.indexOfFirst { itemKey(it) == target.key }.takeIf { it >= 0 }
        } ?: orderedItems.size
        val toIndex = (if (insertionIndex > fromIndex) insertionIndex - 1 else insertionIndex)
            .coerceIn(0, orderedItems.lastIndex)
        if (toIndex != fromIndex) {
            // A lazy list keeps its first visible item in place by key, so
            // moving that item would scroll the row along with it.
            listState.requestScrollToItem(
                listState.firstVisibleItemIndex,
                listState.firstVisibleItemScrollOffset,
            )
            orderedItems = orderedItems.toMutableList().apply {
                add(toIndex, removeAt(fromIndex))
            }
        }
    }

    // Holding the chip near an edge scrolls the row.
    LaunchedEffect(dragging) {
        if (!dragging) return@LaunchedEffect
        val edge = with(density) { AutoScrollEdge.toPx() }
        val maxSpeed = with(density) { AutoScrollMaxSpeed.toPx() }
        fun autoScroll() = listState.autoScroll(dragCenter, liftCenter, orderedItems.lastOrNull()?.let(itemKey), edge)
        // A scroll that moved nothing waits for the finger or the layout to change.
        var stuck: Pair<Float, Float>? = null
        while (true) {
            snapshotFlow { autoScroll() }.first { it != null && it != stuck }
            stuck = null
            var lastFrame = withFrameNanos { it }
            while (true) {
                val frame = withFrameNanos { it }
                val seconds = (frame - lastFrame) / 1_000_000_000f
                lastFrame = frame
                val scroll = autoScroll() ?: break
                val delta = scroll.first * maxSpeed * seconds
                if (listState.scrollBy(if (delta > 0) minOf(delta, scroll.second) else delta) == 0f) {
                    stuck = scroll
                    break
                }
                activeKey?.let(::moveUnderFinger)
            }
        }
    }

    LazyRow(
        state = listState,
        modifier = modifier,
        contentPadding = contentPadding,
        horizontalArrangement = horizontalArrangement,
    ) {
        beforeItems()
        items(orderedItems, key = itemKey) { item ->
            val key = itemKey(item)
            val active = activeKey == key
            val reorderModifier = Modifier
                // Lifted chips follow the finger, not a placement animation.
                .animateItem(placementSpec = if (active) null else placementSpec)
                .then(
                    if (active) {
                        Modifier
                            .zIndex(1f)
                            .graphicsLayer {
                                val lifted = 1f - release.value
                                // Read here, after this frame's layout, so a move never lags a frame.
                                val slotCenter = listState.itemCenter(key)
                                translationX = slotCenter?.let { (dragCenter - it) * lifted } ?: 0f
                                scaleX = 1f + 0.03f * lifted
                                scaleY = 1f + 0.03f * lifted
                                shadowElevation = 8.dp.toPx() * lifted
                            }
                    } else {
                        Modifier
                    },
                )
                .pointerInput(key) {
                    detectDragGesturesAfterLongPress(
                        onDragStart = {
                            val center = listState.itemCenter(key)
                            if (center != null) {
                                settleJob?.cancel()
                                scope.launch { release.snapTo(0f) }
                                activeKey = key
                                dragging = true
                                dragOrigin = orderedItems
                                dragCenter = center
                                liftCenter = center
                                haptic.performHapticFeedback(HapticFeedbackType.GestureThresholdActivate)
                            }
                        },
                        onDragEnd = { if (dragging && activeKey == key) endDrag(commit = true) },
                        onDragCancel = { if (dragging && activeKey == key) endDrag(commit = false) },
                        onDrag = { change, dragAmount ->
                            if (!dragging || activeKey != key) return@detectDragGesturesAfterLongPress
                            change.consume()
                            dragCenter += dragAmount.x

                            moveUnderFinger(key)
                        },
                    )
                }
            itemContent(item, reorderModifier)
        }
        afterItems()
    }
}

private fun LazyListState.itemCenter(key: Any): Float? =
    layoutInfo.visibleItemsInfo.firstOrNull { it.key == key }?.let { it.offset + it.size / 2f }

private val AutoScrollEdge = 48.dp
private val AutoScrollMaxSpeed = 600.dp // per second

/**
 * How deep the lifted chip at [dragCenter] is in the [edge] px at either end of the viewport,
 * from -1 (at the start) to 1 (at the end), and how far the row may scroll
 * toward the end; null outside the zones or when the row cannot scroll that way.
 * A chip lifted at [liftCenter] inside a zone scrolls only once dragged toward that end.
 *
 * The row scrolls toward the end only until the last reorderable item
 * ([lastKey]) is in view: the lifted chip's slot is never past it, and
 * scrolling it out of the viewport would dispose the chip mid-drag.
 */
private fun LazyListState.autoScroll(dragCenter: Float, liftCenter: Float, lastKey: Any?, edge: Float): Pair<Float, Float>? {
    val info = layoutInfo
    val start = info.viewportStartOffset.toFloat()
    val end = info.viewportEndOffset.toFloat()
    return when {
        dragCenter < start + edge && dragCenter < liftCenter && canScrollBackward ->
            -((start + edge - dragCenter) / edge).coerceAtMost(1f) to Float.POSITIVE_INFINITY
        dragCenter > end - edge && dragCenter > liftCenter && canScrollForward -> {
            val last = info.visibleItemsInfo.firstOrNull { it.key == lastKey }
            val limit = last?.let { it.offset + it.size - (end - info.afterContentPadding) } ?: Float.POSITIVE_INFINITY
            if (limit <= 0f) null else ((dragCenter - end + edge) / edge).coerceAtMost(1f) to limit
        }
        else -> null
    }
}
