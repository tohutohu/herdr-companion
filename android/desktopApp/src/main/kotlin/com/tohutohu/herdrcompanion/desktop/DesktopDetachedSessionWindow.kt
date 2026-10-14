package com.tohutohu.herdrcompanion.desktop

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Input
import androidx.compose.material.icons.filled.Close
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.painter.Painter
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyShortcut
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.MenuBar
import androidx.compose.ui.window.Window
import androidx.compose.ui.window.WindowPosition
import androidx.compose.ui.window.rememberWindowState
import com.tohutohu.herdrcompanion.data.api.GatewayApi
import com.tohutohu.herdrcompanion.model.headline
import com.tohutohu.herdrcompanion.ui.theme.SharedTheme
import java.awt.Dimension
import java.awt.event.ComponentAdapter
import java.awt.event.ComponentEvent

/** One session in its own window. Closing the window closes the session, as closing a pane does. */
@Composable
fun DesktopDetachedSessionWindow(
    state: DesktopAppState,
    tearOff: DesktopTearOff,
    api: GatewayApi,
    detached: DesktopDetachedWindow,
    icon: Painter?,
) {
    val sessionId = detached.sessionId
    val windowState = rememberWindowState(
        position = if (detached.x != null && detached.y != null) {
            WindowPosition(detached.x.dp, detached.y.dp)
        } else {
            WindowPosition.PlatformDefault
        },
        width = detached.width.dp,
        height = detached.height.dp,
    )
    Window(
        onCloseRequest = { state.closeSession(sessionId) },
        title = state.detailFor(sessionId)?.session?.headline() ?: "Herdr Companion",
        state = windowState,
        icon = icon,
        // Hidden while a drag holds it over the main window's pane headers, where letting go docks it.
        visible = !(tearOff.draggingSessionId == sessionId && tearOff.dockPreview != null),
    ) {
        window.minimumSize = Dimension(MIN_DETACHED_WIDTH, MIN_DETACHED_HEIGHT)
        DisposableEffect(window) {
            val listener = object : ComponentAdapter() {
                override fun componentMoved(event: ComponentEvent) = record()
                override fun componentResized(event: ComponentEvent) = record()
                private fun record() =
                    state.updateDetachedBounds(sessionId, window.x, window.y, window.width, window.height)
            }
            window.addComponentListener(listener)
            tearOff.attachWindow(sessionId, window)
            onDispose {
                tearOff.detachWindow(window)
                window.removeComponentListener(listener)
            }
        }
        LaunchedEffect(state.windowRaiseRequest) {
            if (state.windowRaiseRequest?.first == sessionId) {
                window.toFront()
                window.requestFocus()
            }
        }
        MenuBar {
            Menu("Window", mnemonic = 'W') {
                Item("Move to Main Window", onClick = { state.returnToMainWindow(sessionId) })
                Separator()
                Item(
                    "Close Window",
                    shortcut = KeyShortcut(Key.W, ctrl = true),
                    onClick = { state.closeSession(sessionId) },
                )
            }
        }
        SharedTheme {
            Surface(
                modifier = Modifier.fillMaxSize(),
                color = MaterialTheme.colorScheme.background,
                contentColor = MaterialTheme.colorScheme.onBackground,
            ) {
                Column(Modifier.fillMaxSize()) {
                    state.transientError?.let { message ->
                        Row(
                            Modifier.fillMaxWidth().padding(horizontal = 18.dp, vertical = 2.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Text(message, color = MaterialTheme.colorScheme.error, modifier = Modifier.weight(1f))
                            TextButton(onClick = state::dismissError) { Text("Dismiss") }
                        }
                    }
                    DesktopSessionContent(
                        state = state,
                        api = api,
                        window = window,
                        sessionId = sessionId,
                        modifier = Modifier.weight(1f).fillMaxWidth(),
                        extraMenuItems = { dismiss ->
                            DropdownMenuItem(
                                text = { Text("Move to Main Window") },
                                leadingIcon = { Icon(Icons.AutoMirrored.Filled.Input, contentDescription = null) },
                                onClick = {
                                    dismiss()
                                    state.returnToMainWindow(sessionId)
                                },
                            )
                            DropdownMenuItem(
                                text = { Text("Close Window") },
                                leadingIcon = { Icon(Icons.Default.Close, contentDescription = null) },
                                onClick = {
                                    dismiss()
                                    state.closeSession(sessionId)
                                },
                            )
                        },
                    )
                }
            }
            if (state.archiveConfirmation?.id == sessionId) ArchiveConfirmationDialog(state)
        }
    }
}
