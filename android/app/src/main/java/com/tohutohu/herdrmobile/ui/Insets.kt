package com.tohutohu.herdrmobile.ui

import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.calculateEndPadding
import androidx.compose.foundation.layout.calculateStartPadding
import androidx.compose.runtime.Composable
import androidx.compose.ui.platform.LocalLayoutDirection

/**
 * These Scaffold paddings without the bottom edge. A list takes that edge as
 * contentPadding instead, so its rows scroll behind the navigation bar and
 * the last one can still be scrolled clear of it.
 */
@Composable
fun PaddingValues.exceptBottom(): PaddingValues {
    val direction = LocalLayoutDirection.current
    return PaddingValues(
        start = calculateStartPadding(direction),
        top = calculateTopPadding(),
        end = calculateEndPadding(direction),
    )
}
