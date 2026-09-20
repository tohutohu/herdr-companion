package com.tohutohu.herdrcompanion.ui

import androidx.compose.ui.graphics.Color

data class StatusStyle(val symbol: String, val label: String, val color: Color)

fun statusStyle(status: String): StatusStyle = when (status) {
    "running" -> StatusStyle("●", "Running", Color(0xFF2E7D32))
    "waiting_input" -> StatusStyle("⚠", "Needs input", Color(0xFFEF6C00))
    "waiting_approval" -> StatusStyle("⚠", "Needs approval", Color(0xFFEF6C00))
    "completed" -> StatusStyle("✓", "Done", Color(0xFF1565C0))
    "idle" -> StatusStyle("○", "Idle", Color(0xFF757575))
    "failed" -> StatusStyle("✕", "Failed", Color(0xFFC62828))
    "offline" -> StatusStyle("–", "Offline", Color(0xFF9E9E9E))
    else -> StatusStyle("?", status, Color(0xFF9E9E9E))
}
