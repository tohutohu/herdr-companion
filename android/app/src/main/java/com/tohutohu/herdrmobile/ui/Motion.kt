package com.tohutohu.herdrmobile.ui

import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.animation.core.CubicBezierEasing
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally

/**
 * Material 3 motion for screen changes: the new screen slides in from the
 * side it belongs to while the old one slides a little the other way, both
 * over a fade (the "shared axis" pattern). Pop transitions mirror it, so a
 * predictive back gesture drags the screen back where it came from.
 */
object ScreenMotion {
    private const val DURATION_MS = 350
    private const val TRAVEL_DIVISOR = 5
    private val emphasizedDecelerate = CubicBezierEasing(0.05f, 0.7f, 0.1f, 1f)
    private val emphasizedAccelerate = CubicBezierEasing(0.3f, 0f, 0.8f, 0.15f)

    val enter: EnterTransition =
        slideInHorizontally(tween(DURATION_MS, easing = emphasizedDecelerate)) { it / TRAVEL_DIVISOR } +
            fadeIn(tween(DURATION_MS / 2, delayMillis = DURATION_MS / 7))

    val exit: ExitTransition =
        slideOutHorizontally(tween(DURATION_MS, easing = emphasizedDecelerate)) { -it / TRAVEL_DIVISOR } +
            fadeOut(tween(DURATION_MS / 3, easing = emphasizedAccelerate))

    val popEnter: EnterTransition =
        slideInHorizontally(tween(DURATION_MS, easing = emphasizedDecelerate)) { -it / TRAVEL_DIVISOR } +
            fadeIn(tween(DURATION_MS / 2, delayMillis = DURATION_MS / 7))

    val popExit: ExitTransition =
        slideOutHorizontally(tween(DURATION_MS, easing = emphasizedDecelerate)) { it / TRAVEL_DIVISOR } +
            fadeOut(tween(DURATION_MS / 3, easing = emphasizedAccelerate))
}
