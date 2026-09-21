package com.tohutohu.herdrcompanion.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

/*
 * The desktop UI is a macOS-style companion window rather than an Android
 * surface. Keep the palette quiet and neutral, and reserve saturated colors
 * for actions and state. The Android app has its own theme in app/ui/Theme.kt.
 */
private val MacLightColorScheme = lightColorScheme(
    primary = Color(0xFF007AFF),
    onPrimary = Color.White,
    primaryContainer = Color(0xFFDCEBFF),
    onPrimaryContainer = Color(0xFF002A5C),
    inversePrimary = Color(0xFF0A84FF),
    secondary = Color(0xFF6E6E73),
    onSecondary = Color.White,
    secondaryContainer = Color(0xFFE5E5EA),
    onSecondaryContainer = Color(0xFF1D1D1F),
    tertiary = Color(0xFFFF9500),
    onTertiary = Color.White,
    tertiaryContainer = Color(0xFFFFE4B5),
    onTertiaryContainer = Color(0xFF3B2100),
    background = Color(0xFFF5F5F7),
    onBackground = Color(0xFF1D1D1F),
    surface = Color.White,
    onSurface = Color(0xFF1D1D1F),
    surfaceVariant = Color(0xFFE5E5EA),
    onSurfaceVariant = Color(0xFF6E6E73),
    surfaceTint = Color(0xFF007AFF),
    inverseSurface = Color(0xFF2C2C2E),
    inverseOnSurface = Color(0xFFF2F2F7),
    error = Color(0xFFFF3B30),
    onError = Color.White,
    errorContainer = Color(0xFFFFE5E5),
    onErrorContainer = Color(0xFF5C0000),
    outline = Color(0xFF8E8E93),
    outlineVariant = Color(0xFFD1D1D6),
    scrim = Color.Black,
    surfaceBright = Color.White,
    surfaceDim = Color(0xFFE5E5EA),
    surfaceContainerLowest = Color.White,
    surfaceContainerLow = Color(0xFFFAFAFC),
    surfaceContainer = Color(0xFFF5F5F7),
    surfaceContainerHigh = Color(0xFFEEEEF0),
    surfaceContainerHighest = Color(0xFFE5E5EA),
)

private val MacDarkColorScheme = darkColorScheme(
    primary = Color(0xFF0A84FF),
    onPrimary = Color.White,
    primaryContainer = Color(0xFF164A7A),
    onPrimaryContainer = Color(0xFFDCEBFF),
    inversePrimary = Color(0xFF007AFF),
    secondary = Color(0xFFAEAEB2),
    onSecondary = Color(0xFF1C1C1E),
    secondaryContainer = Color(0xFF48484A),
    onSecondaryContainer = Color(0xFFF2F2F7),
    tertiary = Color(0xFFFF9F0A),
    onTertiary = Color(0xFF1C1C1E),
    tertiaryContainer = Color(0xFF6B4300),
    onTertiaryContainer = Color(0xFFFFE4B5),
    background = Color(0xFF1C1C1E),
    onBackground = Color(0xFFF2F2F7),
    surface = Color(0xFF2C2C2E),
    onSurface = Color(0xFFF2F2F7),
    surfaceVariant = Color(0xFF3A3A3C),
    onSurfaceVariant = Color(0xFFAEAEB2),
    surfaceTint = Color(0xFF0A84FF),
    inverseSurface = Color(0xFFF2F2F7),
    inverseOnSurface = Color(0xFF1C1C1E),
    error = Color(0xFFFF453A),
    onError = Color.White,
    errorContainer = Color(0xFF5D1A16),
    onErrorContainer = Color(0xFFFFDAD6),
    outline = Color(0xFF8E8E93),
    outlineVariant = Color(0xFF48484A),
    scrim = Color.Black,
    surfaceBright = Color(0xFF3A3A3C),
    surfaceDim = Color(0xFF141416),
    surfaceContainerLowest = Color(0xFF141416),
    surfaceContainerLow = Color(0xFF222224),
    surfaceContainer = Color(0xFF2C2C2E),
    surfaceContainerHigh = Color(0xFF3A3A3C),
    surfaceContainerHighest = Color(0xFF48484A),
)

/** Platform-neutral base theme used by shared UI entry points. */
@Composable
fun SharedTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = if (isSystemInDarkTheme()) MacDarkColorScheme else MacLightColorScheme,
        content = content,
    )
}
