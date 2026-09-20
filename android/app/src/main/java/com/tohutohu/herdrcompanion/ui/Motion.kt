package com.tohutohu.herdrcompanion.ui

import androidx.compose.animation.AnimatedContentTransitionScope
import androidx.compose.animation.ContentTransform
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.animation.core.FastOutLinearInEasing
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.LinearOutSlowInEasing
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.scaleIn
import androidx.compose.animation.scaleOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.animation.togetherWith
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import androidx.navigation3.runtime.metadata
import androidx.navigation3.scene.Scene
import androidx.navigation3.ui.NavDisplay
import androidx.navigationevent.NavigationEvent

private typealias Spec = AnimatedContentTransitionScope<Scene<Route>>.() -> ContentTransform
private typealias PredictiveSpec = AnimatedContentTransitionScope<Scene<Route>>.(Int) -> ContentTransform

/**
 * Screen changes for [NavDisplay].
 *
 * Pushing and popping use Material 3 shared-axis motion: both screens shift a
 * short, fixed distance along the axis while the old one fades out and the
 * new one fades in after it. The travel is kept small and the easing even,
 * and the incoming screen stays transparent for the first frames: those are
 * the ones its first composition may drop, so a late frame shows no jump.
 *
 * A predictive back gesture scrubs a different transition, because a fade
 * that tracks the finger would hide the screen the user is still deciding
 * about: the top screen shrinks and follows the swipe over the one below,
 * which is already in place, and it only fades once the gesture is well along
 * (or committed, when the rest plays out on its own).
 */
class ScreenMotion(density: Density) {
    private val travel = with(density) { 30.dp.roundToPx() }
    private val slide = tween<IntOffset>(DURATION_MS, easing = FastOutSlowInEasing)
    private val fadeInSpec = tween<Float>(DURATION_MS - FADE_OUT_MS, delayMillis = FADE_OUT_MS, easing = LinearOutSlowInEasing)
    private val fadeOutSpec = tween<Float>(FADE_OUT_MS, easing = FastOutLinearInEasing)

    val push: Spec = {
        (slideInHorizontally(slide) { travel } + fadeIn(fadeInSpec)) togetherWith
            (slideOutHorizontally(slide) { -travel } + fadeOut(fadeOutSpec))
    }

    val pop: Spec = {
        (slideInHorizontally(slide) { -travel } + fadeIn(fadeInSpec)) togetherWith
            (slideOutHorizontally(slide) { travel } + fadeOut(fadeOutSpec))
    }

    val predictivePop: PredictiveSpec = { edge ->
        // Swiping from the right edge pulls the screen left, and vice versa.
        val toward = if (edge == NavigationEvent.EDGE_RIGHT) -1 else 1
        val scrub = tween<IntOffset>(DURATION_MS, easing = LinearEasing)
        // The screen below stays put: moving it would uncover the window
        // behind at the corners the shrinking screen no longer covers.
        EnterTransition.None togetherWith (
            scaleOut(tween(DURATION_MS, easing = LinearEasing), targetScale = PEEK_SCALE) +
                slideOutHorizontally(scrub) { toward * travel } +
                fadeOut(tween(DURATION_MS - PEEK_MS, delayMillis = PEEK_MS, easing = LinearEasing))
            )
    }

    companion object {
        private const val DURATION_MS = 300
        private const val FADE_OUT_MS = 90
        private const val PEEK_MS = 180
        private const val PEEK_SCALE = 0.9f

        /**
         * A full-screen viewer (an image) zooms in over the screen that opened
         * it, which stays put underneath, and zooms back out on the way back.
         */
        val zoom: Map<String, Any> = metadata {
            put(NavDisplay.TransitionKey) {
                (fadeIn(tween(150)) + scaleIn(tween(250, easing = FastOutSlowInEasing), initialScale = 0.9f)) togetherWith
                    ExitTransition.KeepUntilTransitionsFinished
            }
            put(NavDisplay.PopTransitionKey) {
                EnterTransition.None togetherWith
                    (fadeOut(tween(150, delayMillis = 50)) + scaleOut(tween(200, easing = FastOutLinearInEasing), targetScale = 0.9f))
            }
            put(NavDisplay.PredictivePopTransitionKey) {
                EnterTransition.None togetherWith
                    (fadeOut(tween(DURATION_MS, easing = LinearEasing)) +
                        scaleOut(tween(DURATION_MS, easing = LinearEasing), targetScale = 0.8f))
            }
        }
    }
}
