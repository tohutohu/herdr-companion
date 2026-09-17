package com.tohutohu.herdrmobile.ui

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.ReadOnlyComposable
import androidx.compose.ui.graphics.Color
import com.tohutohu.herdrmobile.data.api.Status

// A fixed palette instead of Material You: the wallpaper-derived scheme mixed
// light containers with dark on-colors and made cards unreadable.
private val LightColors = lightColorScheme(
    primary = Color(0xFF1F5EA8),
    onPrimary = Color(0xFFFFFFFF),
    primaryContainer = Color(0xFFD6E4FB),
    onPrimaryContainer = Color(0xFF08305E),
    secondary = Color(0xFF4F6075),
    onSecondary = Color(0xFFFFFFFF),
    secondaryContainer = Color(0xFFDCE4EE),
    onSecondaryContainer = Color(0xFF1B2836),
    tertiary = Color(0xFF8A5100),
    onTertiary = Color(0xFFFFFFFF),
    tertiaryContainer = Color(0xFFFFE3BC),
    onTertiaryContainer = Color(0xFF2E1A00),
    error = Color(0xFFB3261E),
    onError = Color(0xFFFFFFFF),
    errorContainer = Color(0xFFF9DEDC),
    onErrorContainer = Color(0xFF410E0B),
    background = Color(0xFFFBFCFE),
    onBackground = Color(0xFF1A1C1E),
    surface = Color(0xFFFBFCFE),
    onSurface = Color(0xFF1A1C1E),
    surfaceVariant = Color(0xFFE2E6EC),
    onSurfaceVariant = Color(0xFF434A52),
    surfaceContainerLowest = Color(0xFFFFFFFF),
    surfaceContainerLow = Color(0xFFF4F6FA),
    surfaceContainer = Color(0xFFEEF1F6),
    surfaceContainerHigh = Color(0xFFE8ECF2),
    surfaceContainerHighest = Color(0xFFE2E7EE),
    outline = Color(0xFF737B84),
    outlineVariant = Color(0xFFC3C9D1),
    inverseSurface = Color(0xFF2F3134),
    inverseOnSurface = Color(0xFFF1F3F6),
    inversePrimary = Color(0xFFA8C7F5),
)

private val DarkColors = darkColorScheme(
    primary = Color(0xFFA8C7F5),
    onPrimary = Color(0xFF0B2F5A),
    primaryContainer = Color(0xFF274A78),
    onPrimaryContainer = Color(0xFFD6E4FB),
    secondary = Color(0xFFB8C7D9),
    onSecondary = Color(0xFF23323F),
    secondaryContainer = Color(0xFF394857),
    onSecondaryContainer = Color(0xFFD6E2F0),
    tertiary = Color(0xFFF2BE72),
    onTertiary = Color(0xFF452A00),
    tertiaryContainer = Color(0xFF61400A),
    onTertiaryContainer = Color(0xFFFFE3BC),
    error = Color(0xFFF2B8B5),
    onError = Color(0xFF601410),
    errorContainer = Color(0xFF8C1D18),
    onErrorContainer = Color(0xFFF9DEDC),
    background = Color(0xFF111417),
    onBackground = Color(0xFFE3E6EA),
    surface = Color(0xFF111417),
    onSurface = Color(0xFFE3E6EA),
    surfaceVariant = Color(0xFF3A4149),
    onSurfaceVariant = Color(0xFFC2C9D2),
    surfaceContainerLowest = Color(0xFF0C0F11),
    surfaceContainerLow = Color(0xFF181B1F),
    surfaceContainer = Color(0xFF1C2024),
    surfaceContainerHigh = Color(0xFF262A2F),
    surfaceContainerHighest = Color(0xFF31363B),
    outline = Color(0xFF8C949D),
    outlineVariant = Color(0xFF434A52),
    inverseSurface = Color(0xFFE3E6EA),
    inverseOnSurface = Color(0xFF2C2F33),
    inversePrimary = Color(0xFF1F5EA8),
)

@Composable
fun HerdrTheme(content: @Composable () -> Unit) {
    MaterialTheme(colorScheme = if (isSystemInDarkTheme()) DarkColors else LightColors, content = content)
}

data class StatusStyle(val symbol: String, val label: String, val color: Color)

@Composable
@ReadOnlyComposable
fun statusStyle(status: String): StatusStyle {
    val dark = isSystemInDarkTheme()
    fun color(light: Long, onDark: Long) = Color(if (dark) onDark else light)
    return when (status) {
        Status.RUNNING -> StatusStyle("●", "Running", color(0xFF2E7D32, 0xFF7CC47F))
        Status.WAITING_INPUT -> StatusStyle("⚠", "Needs input", color(0xFFA85200, 0xFFF2BE72))
        Status.WAITING_APPROVAL -> StatusStyle("⚠", "Needs approval", color(0xFFA85200, 0xFFF2BE72))
        Status.COMPLETED -> StatusStyle("✓", "Done", color(0xFF1565C0, 0xFF8FBDF0))
        Status.IDLE -> StatusStyle("○", "Idle", color(0xFF6B7278, 0xFF9AA2AA))
        Status.FAILED -> StatusStyle("✕", "Failed", color(0xFFC62828, 0xFFF2B8B5))
        Status.OFFLINE -> StatusStyle("–", "Offline", color(0xFF8A9199, 0xFF7E868E))
        else -> StatusStyle("?", status, color(0xFF8A9199, 0xFF7E868E))
    }
}
