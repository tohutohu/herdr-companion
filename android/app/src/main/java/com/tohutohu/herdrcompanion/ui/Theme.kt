package com.tohutohu.herdrcompanion.ui

import android.app.Activity
import android.os.Build
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.ColorScheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.SideEffect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalView
import androidx.core.view.WindowCompat
import com.tohutohu.herdrcompanion.data.ThemeMode

/*
 * Dynamic Color is intentionally kept for accents and state. Its wallpaper
 * seed does not get to tint every reading surface, because a warm seed can
 * otherwise turn the whole app orange.
 */
private val HerdrLightFallbackColorScheme = lightColorScheme(
    primary = Color(0xFFAD5B18),
    onPrimary = Color.White,
    primaryContainer = Color(0xFFF2E9E3),
    onPrimaryContainer = Color(0xFF3B1B08),
    inversePrimary = Color(0xFFFFB783),
    secondary = Color(0xFF5E6872),
    onSecondary = Color.White,
    secondaryContainer = Color(0xFFE2E6EA),
    onSecondaryContainer = Color(0xFF1A1D21),
    tertiary = Color(0xFF9A6800),
    onTertiary = Color.White,
    tertiaryContainer = Color(0xFFECE9E6),
    onTertiaryContainer = Color(0xFF2E2925),
    background = Color(0xFFF7F7F5),
    onBackground = Color(0xFF1B1D1F),
    surface = Color.White,
    onSurface = Color(0xFF1B1D1F),
    surfaceVariant = Color(0xFFE8EAEC),
    onSurfaceVariant = Color(0xFF5F646B),
    surfaceTint = Color(0xFFAD5B18),
    inverseSurface = Color(0xFF2F3338),
    inverseOnSurface = Color(0xFFF1F2F3),
    error = Color(0xFFBA1A1A),
    onError = Color.White,
    errorContainer = Color(0xFFFFDAD6),
    onErrorContainer = Color(0xFF410002),
    outline = Color(0xFF777D84),
    outlineVariant = Color(0xFFCDD1D6),
    scrim = Color.Black,
    surfaceBright = Color.White,
    surfaceDim = Color(0xFFDFE1E3),
    surfaceContainerLowest = Color.White,
    surfaceContainerLow = Color(0xFFFBFBFA),
    surfaceContainer = Color(0xFFF4F5F5),
    surfaceContainerHigh = Color(0xFFECEEEF),
    surfaceContainerHighest = Color(0xFFE4E6E8),
)

private val HerdrDarkFallbackColorScheme = darkColorScheme(
    primary = Color(0xFFF3A66F),
    onPrimary = Color(0xFF4B210A),
    primaryContainer = Color(0xFF3C302A),
    onPrimaryContainer = Color(0xFFFFE0CA),
    inversePrimary = Color(0xFFAD5B18),
    secondary = Color(0xFFB8C0C8),
    onSecondary = Color(0xFF273038),
    secondaryContainer = Color(0xFF394148),
    onSecondaryContainer = Color(0xFFDFE5EA),
    tertiary = Color(0xFFE0B36B),
    onTertiary = Color(0xFF3A2808),
    tertiaryContainer = Color(0xFF3A3430),
    onTertiaryContainer = Color(0xFFEDE7E2),
    background = Color(0xFF111315),
    onBackground = Color(0xFFE7E9EC),
    surface = Color(0xFF181A1D),
    onSurface = Color(0xFFE7E9EC),
    surfaceVariant = Color(0xFF2B3035),
    onSurfaceVariant = Color(0xFFB8BEC5),
    surfaceTint = Color(0xFFF3A66F),
    inverseSurface = Color(0xFFE7E9EC),
    inverseOnSurface = Color(0xFF2C3034),
    error = Color(0xFFFFB4AB),
    onError = Color(0xFF690005),
    errorContainer = Color(0xFF5C211B),
    onErrorContainer = Color(0xFFFFDAD6),
    outline = Color(0xFF899198),
    outlineVariant = Color(0xFF454B51),
    scrim = Color.Black,
    surfaceBright = Color(0xFF3B4147),
    surfaceDim = Color(0xFF111315),
    surfaceContainerLowest = Color(0xFF0D0F11),
    surfaceContainerLow = Color(0xFF151719),
    surfaceContainer = Color(0xFF1D2023),
    surfaceContainerHigh = Color(0xFF25292D),
    surfaceContainerHighest = Color(0xFF2D3237),
)

private fun ColorScheme.withNeutralSurfaces(dark: Boolean): ColorScheme {
    val background = if (dark) Color(0xFF111315) else Color(0xFFF7F7F5)
    val onBackground = if (dark) Color(0xFFE7E9EC) else Color(0xFF1B1D1F)
    val surface = if (dark) Color(0xFF181A1D) else Color.White
    val onSurface = if (dark) Color(0xFFE7E9EC) else Color(0xFF1B1D1F)
    val surfaceVariant = if (dark) Color(0xFF2B3035) else Color(0xFFE8EAEC)
    val onSurfaceVariant = if (dark) Color(0xFFB8BEC5) else Color(0xFF5F646B)
    val outline = if (dark) Color(0xFF899198) else Color(0xFF777D84)
    val outlineVariant = if (dark) Color(0xFF454B51) else Color(0xFFCDD1D6)

    return copy(
        // Keep dynamic primary/secondary/tertiary roles for accents.
        background = background,
        onBackground = onBackground,
        surface = surface,
        onSurface = onSurface,
        surfaceVariant = surfaceVariant,
        onSurfaceVariant = onSurfaceVariant,
        secondaryContainer = if (dark) Color(0xFF394148) else Color(0xFFE2E6EA),
        onSecondaryContainer = if (dark) Color(0xFFDFE5EA) else Color(0xFF1A1D21),
        // Pending/approval cards should be readable, not large orange blocks.
        tertiaryContainer = if (dark) Color(0xFF3A3430) else Color(0xFFECE9E6),
        onTertiaryContainer = if (dark) Color(0xFFEDE7E2) else Color(0xFF2E2925),
        outline = outline,
        outlineVariant = outlineVariant,
        inverseSurface = if (dark) Color(0xFFE7E9EC) else Color(0xFF2F3338),
        inverseOnSurface = if (dark) Color(0xFF2C3034) else Color(0xFFF1F2F3),
        surfaceBright = if (dark) Color(0xFF3B4147) else Color.White,
        surfaceDim = if (dark) Color(0xFF111315) else Color(0xFFDFE1E3),
        surfaceContainerLowest = if (dark) Color(0xFF0D0F11) else Color.White,
        surfaceContainerLow = if (dark) Color(0xFF151719) else Color(0xFFFBFBFA),
        surfaceContainer = if (dark) Color(0xFF1D2023) else Color(0xFFF4F5F5),
        surfaceContainerHigh = if (dark) Color(0xFF25292D) else Color(0xFFECEEEF),
        surfaceContainerHighest = if (dark) Color(0xFF2D3237) else Color(0xFFE4E6E8),
    )
}

@Composable
fun HerdrTheme(themeMode: ThemeMode = ThemeMode.SYSTEM, content: @Composable () -> Unit) {
    val dark = when (themeMode) {
        ThemeMode.SYSTEM -> isSystemInDarkTheme()
        ThemeMode.LIGHT -> false
        ThemeMode.DARK -> true
    }
    val context = LocalContext.current
    val colors = when {
        Build.VERSION.SDK_INT >= Build.VERSION_CODES.S -> {
            val dynamic = if (dark) dynamicDarkColorScheme(context) else dynamicLightColorScheme(context)
            dynamic.withNeutralSurfaces(dark)
        }
        dark -> HerdrDarkFallbackColorScheme
        else -> HerdrLightFallbackColorScheme
    }
    val view = LocalView.current
    SideEffect {
        (view.context as? Activity)?.window?.let { window ->
            WindowCompat.getInsetsController(window, view).apply {
                isAppearanceLightStatusBars = !dark
                isAppearanceLightNavigationBars = !dark
            }
        }
    }
    MaterialTheme(colorScheme = colors, content = content)
}
