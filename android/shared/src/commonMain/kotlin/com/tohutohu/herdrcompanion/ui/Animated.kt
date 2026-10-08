package com.tohutohu.herdrcompanion.ui

import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.SizeTransform
import androidx.compose.animation.core.LinearEasing
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
        enter = expandVertically(UiMotion.resize()) + fadeIn(tween(UiMotion.FadeInMs, easing = LinearEasing)),
        exit = shrinkVertically(UiMotion.resize()) + fadeOut(tween(UiMotion.FadeOutMs, easing = LinearEasing)),
    ) {
        last?.let { content(it) }
    }
}

/**
 * Fades between states without scaling text. Icon-only controls can opt into
 * a small scale change; any size change settles in the same time as a panel.
 */
@Composable
fun <T> SwapContent(
    target: T,
    modifier: Modifier = Modifier,
    iconOnly: Boolean = false,
    content: @Composable (T) -> Unit,
) {
    AnimatedContent(
        targetState = target,
        modifier = modifier,
        contentAlignment = Alignment.Center,
        transitionSpec = {
            val enter = fadeIn(tween(UiMotion.FadeInMs, easing = LinearEasing))
            val exit = fadeOut(tween(UiMotion.FadeOutMs, easing = LinearEasing))
            (if (iconOnly) enter + scaleIn(UiMotion.feedback(), initialScale = 0.94f) else enter)
                .togetherWith(if (iconOnly) exit + scaleOut(UiMotion.feedback(), targetScale = 0.94f) else exit)
                .using(SizeTransform(clip = false) { _, _ -> UiMotion.resize() })
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
    val rotation by animateFloatAsState(if (expanded) 180f else 0f, UiMotion.resize(), label = "chevron")
    Icon(
        Icons.Default.ExpandMore,
        contentDescription = contentDescription,
        tint = tint,
        modifier = modifier.graphicsLayer { rotationZ = rotation },
    )
}
