package com.tohutohu.herdrcompanion.ui.sessions

import androidx.compose.foundation.background
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.FormatColorReset
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp

/** Stable keys are persisted locally; never persist enum ordinals or theme colors. */
enum class SessionColor(val key: String, val label: String, val tint: Color) {
    Red("red", "Red", Color(0xFFE57373)),
    Orange("orange", "Orange", Color(0xFFFFB74D)),
    Green("green", "Green", Color(0xFF81C784)),
    Blue("blue", "Blue", Color(0xFF64B5F6)),
    Purple("purple", "Purple", Color(0xFFBA68C8)),
    Pink("pink", "Pink", Color(0xFFF06292));

    companion object {
        fun fromKey(key: String?): SessionColor? = entries.firstOrNull { it.key == key }
    }
}

@Composable
fun SessionColorDialog(
    onDismiss: () -> Unit,
    onSelect: (SessionColor?) -> Unit,
    current: SessionColor? = null,
    mixed: Boolean = false,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Session color") },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState())) {
                (listOf<SessionColor?>(null) + SessionColor.entries).forEach { color ->
                    DropdownMenuItem(
                        text = { Text(color?.label ?: "No color") },
                        leadingIcon = {
                            if (color == null) Icon(Icons.Default.FormatColorReset, contentDescription = null)
                            else Spacer(Modifier.size(20.dp).background(color.tint, CircleShape))
                        },
                        trailingIcon = {
                            if (!mixed && color == current) Icon(Icons.Default.Check, contentDescription = "Current color")
                        },
                        onClick = { onSelect(color) },
                    )
                }
            }
        },
        confirmButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}
