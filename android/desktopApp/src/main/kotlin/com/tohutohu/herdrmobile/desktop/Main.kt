package com.tohutohu.herdrmobile.desktop

import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.window.Window
import androidx.compose.ui.window.application
import com.tohutohu.herdrmobile.ui.theme.SharedTheme

fun main() = application {
    Window(
        onCloseRequest = ::exitApplication,
        title = "Herdr",
    ) {
        SharedTheme {
            DesktopPlaceholder()
        }
    }
}

@Composable
private fun DesktopPlaceholder() {
    Text("Herdr shared UI")
}
