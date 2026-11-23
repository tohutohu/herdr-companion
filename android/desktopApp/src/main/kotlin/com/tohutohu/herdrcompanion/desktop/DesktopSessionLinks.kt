package com.tohutohu.herdrcompanion.desktop

import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.platform.UriHandler
import com.tohutohu.herdrcompanion.data.SessionLink
import java.awt.Desktop

/**
 * Hands `hc://session/<id>` links the OS delivers to this app to [onOpen], on
 * the event thread. A link that launched the app is queued by AWT until this
 * handler is installed. Only macOS delivers links this way.
 */
internal fun installSessionLinkHandler(onOpen: (String) -> Unit) {
    if (!Desktop.isDesktopSupported()) return
    val desktop = Desktop.getDesktop()
    if (!desktop.isSupported(Desktop.Action.APP_OPEN_URI)) return
    desktop.setOpenURIHandler { event -> SessionLink.sessionId(event.uri.toString())?.let(onOpen) }
}

/**
 * Opens session links tapped in [content] (a message's `[label](hc://session/<id>)`)
 * with [onOpen] instead of the OS, which only routes them back on macOS.
 */
@Composable
internal fun SessionLinkUris(onOpen: (String) -> Unit, content: @Composable () -> Unit) {
    val platform = LocalUriHandler.current
    val uris = remember(platform, onOpen) {
        object : UriHandler {
            override fun openUri(uri: String) {
                SessionLink.sessionId(uri)?.let(onOpen) ?: platform.openUri(uri)
            }
        }
    }
    CompositionLocalProvider(LocalUriHandler provides uris, content = content)
}
