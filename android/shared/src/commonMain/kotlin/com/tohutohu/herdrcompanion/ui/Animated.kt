package com.tohutohu.herdrcompanion.ui

import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.scaleIn
import androidx.compose.animation.scaleOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.animation.togetherWith
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ExpandMore
import androidx.compose.material3.Icon
import androidx.compose.material3.LocalContentColor
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer

/**
 * Shows [content] for [value] while it is non-null, expanding it into place
 * and shrinking it away. While shrinking, the content keeps the last value it
 * had, so an error line or a panel does not blank out before it is gone.
 */
@Composable
fun <T : Any> ExpandingContent(value: T?, modifier: Modifier = Modifier, content: @Composable (T) -> Unit) {
    var last by remember { mutableStateOf(value) }
    if (value != null) last = value
    AnimatedVisibility(
        visible = value != null,
        modifier = modifier,
        enter = expandVertically() + fadeIn(),
        exit = shrinkVertically() + fadeOut(),
    ) {
        last?.let { content(it) }
    }
}

/**
 * Swaps between two states of the same control (a button and its spinner, an
 * icon and its counterpart): the old one scales and fades away as the new one
 * scales in, centred.
 */
@Composable
fun <T> SwapContent(target: T, modifier: Modifier = Modifier, content: @Composable (T) -> Unit) {
    AnimatedContent(
        targetState = target,
        modifier = modifier,
        contentAlignment = Alignment.Center,
        transitionSpec = {
            (fadeIn(tween(160)) + scaleIn(tween(160), initialScale = 0.8f))
                .togetherWith(fadeOut(tween(100)) + scaleOut(tween(100), targetScale = 0.8f))
        },
        label = "swap",
    ) { content(it) }
}

/** A chevron that turns upside down when [expanded], instead of being swapped. */
@Composable
fun ExpandChevron(
    expanded: Boolean,
    contentDescription: String?,
    modifier: Modifier = Modifier,
    tint: Color = LocalContentColor.current,
) {
    val rotation by animateFloatAsState(if (expanded) 180f else 0f, label = "chevron")
    Icon(
        Icons.Default.ExpandMore,
        contentDescription = contentDescription,
        tint = tint,
        modifier = modifier.graphicsLayer { rotationZ = rotation },
    )
}
