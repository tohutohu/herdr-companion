package com.tohutohu.herdrmobile.ui

import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.animation.core.FastOutLinearInEasing
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.LinearOutSlowInEasing
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp

/**
 * Material 3 shared-axis motion for screen changes: both screens shift a
 * short, fixed distance along the axis while the old one fades out and the
 * new one fades in after it. Pop transitions mirror it, so a predictive back
 * gesture drags the screen back where it came from.
 *
 * The travel is kept small and the easing even, and the incoming screen stays
 * transparent for the first frames: those are the ones its first composition
 * may drop, so a late frame shows no jump.
 */
class ScreenMotion(density: Density) {
    private val travel = with(density) { 30.dp.roundToPx() }
    private val slide = tween<IntOffset>(DURATION_MS, easing = FastOutSlowInEasing)
    private val fadeInSpec = tween<Float>(DURATION_MS - FADE_OUT_MS, delayMillis = FADE_OUT_MS, easing = LinearOutSlowInEasing)
    private val fadeOutSpec = tween<Float>(FADE_OUT_MS, easing = FastOutLinearInEasing)

    val enter: EnterTransition = slideInHorizontally(slide) { travel } + fadeIn(fadeInSpec)
    val exit: ExitTransition = slideOutHorizontally(slide) { -travel } + fadeOut(fadeOutSpec)
    val popEnter: EnterTransition = slideInHorizontally(slide) { -travel } + fadeIn(fadeInSpec)
    val popExit: ExitTransition = slideOutHorizontally(slide) { travel } + fadeOut(fadeOutSpec)

    private companion object {
        const val DURATION_MS = 300
        const val FADE_OUT_MS = 90
    }
}
