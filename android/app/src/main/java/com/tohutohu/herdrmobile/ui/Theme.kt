package com.tohutohu.herdrmobile.ui

import android.os.Build
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import com.tohutohu.herdrmobile.data.api.Status

@Composable
fun HerdrTheme(content: @Composable () -> Unit) {
    val dark = isSystemInDarkTheme()
    val context = LocalContext.current
    val colors = when {
        Build.VERSION.SDK_INT >= Build.VERSION_CODES.S ->
            if (dark) dynamicDarkColorScheme(context) else dynamicLightColorScheme(context)
        dark -> darkColorScheme()
        else -> lightColorScheme()
    }
    MaterialTheme(colorScheme = colors, content = content)
}

data class StatusStyle(val symbol: String, val label: String, val color: Color)

fun statusStyle(status: String): StatusStyle = when (status) {
    Status.RUNNING -> StatusStyle("●", "Running", Color(0xFF2E7D32))
    Status.WAITING_INPUT -> StatusStyle("⚠", "Needs input", Color(0xFFEF6C00))
    Status.WAITING_APPROVAL -> StatusStyle("⚠", "Needs approval", Color(0xFFEF6C00))
    Status.COMPLETED -> StatusStyle("✓", "Done", Color(0xFF1565C0))
    Status.IDLE -> StatusStyle("○", "Idle", Color(0xFF757575))
    Status.FAILED -> StatusStyle("✕", "Failed", Color(0xFFC62828))
    Status.OFFLINE -> StatusStyle("–", "Offline", Color(0xFF9E9E9E))
    else -> StatusStyle("?", status, Color(0xFF9E9E9E))
}
