package com.tohutohu.herdrcompanion.ui

import androidx.compose.animation.core.FiniteAnimationSpec
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.VisibilityThreshold
import androidx.compose.animation.core.spring
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.unit.IntOffset
import kotlinx.coroutines.delay

/** How long placement animations stay paused after an item resizes. */
const val PLACEMENT_PAUSE_MS = 300L

/**
 * When a lazy list item changes size, the items after it move in that same
 * frame. Animated as placements (`animateItem`), those moves restart every
 * frame of the resize, so the items trail behind it and overlap it. Lists
 * pass `placementSpec = null` while this is true.
 *
 * It is true from the composition in which [value] changes as told by
 * [changed], which is before that change is laid out, until
 * [PLACEMENT_PAUSE_MS] later.
 */
@Composable
fun <T> pausePlacementOnChange(value: T, changed: (before: T, after: T) -> Boolean = { a, b -> a != b }): Boolean {
    val last = remember { arrayOf<Any?>(value) }
    val justChanged = remember(value) {
        @Suppress("UNCHECKED_CAST")
        changed(last[0] as T, value).also { last[0] = value }
    }
    // A new identity for every distinct value, to tell when its pause ended.
    val stamp = remember(value) { Any() }
    var settled by remember { mutableStateOf<Any?>(null) }
    LaunchedEffect(stamp) {
        if (justChanged) {
            delay(PLACEMENT_PAUSE_MS)
            settled = stamp
        }
    }
    return justChanged && settled !== stamp
}

/** `animateItem`'s own placement spec, or none while [paused]. */
fun itemPlacementSpec(paused: Boolean): FiniteAnimationSpec<IntOffset>? =
    if (paused) null else spring(stiffness = Spring.StiffnessMediumLow, visibilityThreshold = IntOffset.VisibilityThreshold)

/**
 * Whether an item present in both lists has new content while the shared
 * items keep their order. A reorder is left to the placement animations it
 * needs; added and removed items are not changes in place.
 */
fun <T> changedInPlace(before: List<T>, after: List<T>, key: (T) -> Any): Boolean {
    if (before.isEmpty() || after.isEmpty()) return false
    val old = before.associateBy(key)
    val common = after.filter { key(it) in old }
    val commonKeys = common.map(key)
    val shared = commonKeys.toSet()
    if (before.map(key).filter { it in shared } != commonKeys) return false
    return common.any { old[key(it)] != it }
}
