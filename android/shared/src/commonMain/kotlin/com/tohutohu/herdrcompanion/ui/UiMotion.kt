package com.tohutohu.herdrcompanion.ui

import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.tween

/** Small, non-bouncing changes inside a screen. Navigation keeps its own motion. */
internal object UiMotion {
    const val ResizeMs = 200
    const val FadeInMs = 140
    const val FadeOutMs = 90

    fun <T> resize() = tween<T>(ResizeMs, easing = FastOutSlowInEasing)
    fun <T> feedback() = tween<T>(FadeInMs, easing = FastOutSlowInEasing)
}
