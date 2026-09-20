package com.tohutohu.herdrcompanion.ui.newsession

import androidx.compose.foundation.gestures.detectDragGesturesAfterLongPress
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.lazy.LazyItemScope
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.unit.dp
import androidx.compose.ui.zIndex

/**
 * A horizontal row whose [rowItems] can be reordered by long-pressing and
 * dragging. Content added by [beforeItems] and [afterItems] stays fixed and
 * is not part of the reorderable range.
 */
@Composable
fun <T> ReorderableChipRow(
    rowItems: List<T>,
    itemKey: (T) -> Any,
    onMove: (List<T>) -> Unit,
    modifier: Modifier = Modifier,
    contentPadding: PaddingValues = PaddingValues(),
    horizontalArrangement: Arrangement.Horizontal = Arrangement.Start,
    beforeItems: LazyListScope.() -> Unit = {},
    afterItems: LazyListScope.() -> Unit = {},
    itemContent: @Composable LazyItemScope.(T, Modifier) -> Unit,
) {
    val listState = rememberLazyListState()
    var orderedItems by remember { mutableStateOf(rowItems) }
    var draggedKey by remember { mutableStateOf<Any?>(null) }
    var dragOrigin by remember { mutableStateOf<List<T>?>(null) }
    var dragStartCenter by remember { mutableStateOf(0f) }
    var dragDistance by remember { mutableStateOf(0f) }
    val haptic = LocalHapticFeedback.current

    LaunchedEffect(rowItems) {
        if (draggedKey == null) orderedItems = rowItems
    }

    fun endDrag(commit: Boolean) {
        val origin = dragOrigin
        val result = orderedItems
        if (!commit && origin != null) orderedItems = origin
        draggedKey = null
        dragOrigin = null
        dragDistance = 0f
        if (commit && origin != null && origin != result) onMove(result)
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
            val dragging = draggedKey == key
            val translation = if (dragging) {
                val info = listState.layoutInfo.visibleItemsInfo.firstOrNull { it.key == key }
                info?.let { dragStartCenter + dragDistance - (it.offset + it.size / 2f) } ?: dragDistance
            } else {
                0f
            }
            val reorderModifier = Modifier
                .then(
                    if (dragging) {
                        Modifier
                            .zIndex(1f)
                            .graphicsLayer {
                                translationX = translation
                                scaleX = 1.03f
                                scaleY = 1.03f
                                shadowElevation = 8.dp.toPx()
                            }
                    } else {
                        Modifier
                    },
                )
                .pointerInput(key) {
                    detectDragGesturesAfterLongPress(
                        onDragStart = {
                            val info = listState.layoutInfo.visibleItemsInfo.firstOrNull { it.key == key }
                            if (info != null) {
                                draggedKey = key
                                dragOrigin = orderedItems
                                dragStartCenter = info.offset + info.size / 2f
                                dragDistance = 0f
                                haptic.performHapticFeedback(HapticFeedbackType.GestureThresholdActivate)
                            }
                        },
                        onDragEnd = { if (draggedKey == key) endDrag(commit = true) },
                        onDragCancel = { if (draggedKey == key) endDrag(commit = false) },
                        onDrag = { change, dragAmount ->
                            if (draggedKey != key) return@detectDragGesturesAfterLongPress
                            change.consume()
                            dragDistance += dragAmount.x

                            val fromIndex = orderedItems.indexOfFirst { itemKey(it) == key }
                            if (fromIndex < 0) return@detectDragGesturesAfterLongPress

                            val dragCenter = dragStartCenter + dragDistance
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
                                orderedItems = orderedItems.toMutableList().apply {
                                    add(toIndex, removeAt(fromIndex))
                                }
                            }
                        },
                    )
                }
            itemContent(item, reorderModifier)
        }
        afterItems()
    }
}
