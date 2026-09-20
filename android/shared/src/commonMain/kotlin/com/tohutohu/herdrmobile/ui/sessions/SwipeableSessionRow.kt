package com.tohutohu.herdrmobile.ui.sessions

import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.spring
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Archive
import androidx.compose.material.icons.filled.StopCircle
import androidx.compose.material.icons.filled.Unarchive
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.SwipeToDismissBox
import androidx.compose.material3.SwipeToDismissBoxDefaults
import androidx.compose.material3.SwipeToDismissBoxState
import androidx.compose.material3.SwipeToDismissBoxValue
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.flow.drop

/**
 * A session row that swipes sideways into [action]. The row stays swiped
 * open while the action runs (or waits for its confirmation) and slides back
 * if it is declined or fails; on success the row leaves the list, and the
 * list's item animation takes it from there.
 *
 * [engaged] reports whether the action owns the row right now (running or
 * waiting for its confirmation); [isEngaged] reads the same thing outside
 * composition, right after [onSwipe] returns. [busy] shows progress.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun SwipeableSessionRow(
    action: SwipeAction,
    engaged: Boolean,
    isEngaged: () -> Boolean,
    busy: Boolean,
    enabled: Boolean,
    onSwipe: () -> Unit,
    modifier: Modifier = Modifier,
    content: @Composable () -> Unit,
) {
    // Not rememberSaveable: a row that comes back after an undo (same key)
    // must start settled, not swiped, or it would fire the action again.
    val density = LocalDensity.current
    val threshold = SwipeToDismissBoxDefaults.positionalThreshold
    val state = remember(density) {
        SwipeToDismissBoxState(SwipeToDismissBoxValue.Settled, density, { true }, threshold)
    }
    val haptic = LocalHapticFeedback.current

    // Tick when the drag crosses the release threshold.
    LaunchedEffect(state) {
        snapshotFlow { state.targetValue }.drop(1).collect {
            if (it != SwipeToDismissBoxValue.Settled && state.currentValue == SwipeToDismissBoxValue.Settled) {
                haptic.performHapticFeedback(HapticFeedbackType.GestureThresholdActivate)
            }
        }
    }
    // Settled off screen: hand the row to the action, or slide back if it
    // declined it (e.g. the same session is already being changed).
    LaunchedEffect(state.currentValue) {
        if (state.currentValue == SwipeToDismissBoxValue.Settled) return@LaunchedEffect
        onSwipe()
        if (!isEngaged()) state.reset()
    }
    // The action let go of the row (or its confirmation was canceled) and it
    // is still listed: slide back. targetValue matters while the dismiss
    // animation is still settling and currentValue is still Settled.
    LaunchedEffect(engaged) {
        if (!engaged && state.targetValue != SwipeToDismissBoxValue.Settled) state.reset()
    }

    SwipeToDismissBox(
        state = state,
        modifier = modifier,
        gesturesEnabled = enabled && !engaged,
        backgroundContent = {
            SwipeBackground(
                action,
                reached = state.targetValue != SwipeToDismissBoxValue.Settled,
                direction = state.dismissDirection,
                busy = busy,
            )
        },
    ) {
        content()
    }
}

@Composable
private fun SwipeBackground(action: SwipeAction, reached: Boolean, direction: SwipeToDismissBoxValue, busy: Boolean) {
    val scheme = MaterialTheme.colorScheme
    val (container, onContainer) = when (action) {
        SwipeAction.ARCHIVE -> scheme.tertiaryContainer to scheme.onTertiaryContainer
        SwipeAction.STOP_AND_ARCHIVE -> scheme.errorContainer to scheme.onErrorContainer
        SwipeAction.UNARCHIVE -> scheme.primaryContainer to scheme.onPrimaryContainer
    }
    val background by animateColorAsState(if (reached) container else scheme.surfaceContainerHighest, label = "swipeBackground")
    val tint by animateColorAsState(if (reached) onContainer else scheme.onSurfaceVariant, label = "swipeTint")
    val scale by animateFloatAsState(
        if (reached) 1f else 0.7f,
        spring(dampingRatio = Spring.DampingRatioMediumBouncy, stiffness = Spring.StiffnessMediumLow),
        label = "swipeIcon",
    )
    val icon = when (action) {
        SwipeAction.ARCHIVE -> Icons.Default.Archive
        SwipeAction.STOP_AND_ARCHIVE -> Icons.Default.StopCircle
        SwipeAction.UNARCHIVE -> Icons.Default.Unarchive
    }
    Box(Modifier.fillMaxSize().background(background)) {
        Row(
            Modifier
                .align(if (direction == SwipeToDismissBoxValue.StartToEnd) Alignment.CenterStart else Alignment.CenterEnd)
                .padding(horizontal = 24.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            if (direction == SwipeToDismissBoxValue.EndToStart) {
                Text(action.label, color = tint, style = MaterialTheme.typography.labelLarge)
            }
            Icon(
                icon,
                contentDescription = action.label,
                tint = tint,
                modifier = Modifier.graphicsLayer {
                    scaleX = scale
                    scaleY = scale
                },
            )
            if (direction != SwipeToDismissBoxValue.EndToStart) {
                Text(action.label, color = tint, style = MaterialTheme.typography.labelLarge)
            }
        }
        if (busy) LinearProgressIndicator(Modifier.fillMaxWidth().align(Alignment.BottomCenter))
    }
}
