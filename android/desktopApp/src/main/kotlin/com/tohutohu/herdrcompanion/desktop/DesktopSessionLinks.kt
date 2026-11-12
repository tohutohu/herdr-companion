package com.tohutohu.herdrcompanion.desktop

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
