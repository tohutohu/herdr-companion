package com.tohutohu.herdrcompanion.desktop

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import java.awt.AWTEvent
import java.awt.Point
import java.awt.Rectangle
import java.awt.Toolkit
import java.awt.Window
import java.awt.event.AWTEventListener
import java.awt.event.MouseEvent

/** The main window's pane header strip, where a dragged-out session can be dropped back in. */
fun interface DesktopPaneDropZone {
    /** The pane slot a session released at [point] (screen points) would take, or null outside the strip. */
    fun indexAt(point: Point): Int?
}

/**
 * Carries a pane header drag on after the pane has left the main window, the
 * way a browser tab follows the pointer out of its window. Compose drops the
 * gesture with the pane, but AWT keeps sending the drag and the release to the
 * window where the press happened, even outside its bounds, so a toolkit
 * listener moves the new window and docks it back when it is released over
 * the main window's pane headers.
 */
class DesktopTearOff(private val state: DesktopAppState) {
    /** The pointer's last position on screen, in points. */
    @Volatile
    var pointerOnScreen: Point? = null
        private set

    var draggingSessionId by mutableStateOf<String?>(null)
        private set

    /** The slot the dragged session takes if released now; null while it is outside the drop zone. */
    var dropIndex by mutableStateOf<Int?>(null)
        private set

    var dropZone: DesktopPaneDropZone? = null

    private var grab = Point()
    private var window: Window? = null

    private val listener = AWTEventListener { event ->
        val mouse = event as? MouseEvent ?: return@AWTEventListener
        val point = mouse.locationOnScreen
        pointerOnScreen = point
        if (draggingSessionId == null) return@AWTEventListener
        when (mouse.id) {
            MouseEvent.MOUSE_DRAGGED -> follow(point)
            // A press means the release was lost, e.g. to another app; end there.
            MouseEvent.MOUSE_RELEASED, MouseEvent.MOUSE_PRESSED -> finish(point)
        }
    }

    init {
        Toolkit.getDefaultToolkit().addAWTEventListener(
            listener,
            AWTEvent.MOUSE_EVENT_MASK or AWTEvent.MOUSE_MOTION_EVENT_MASK,
        )
    }

    /**
     * Moves a pane into its own window that follows the pointer until the
     * button is released. [grabX] and [grabY] are where the pointer holds the
     * window, measured from its top-left corner.
     */
    fun tearOff(sessionId: String, grabX: Int, grabY: Int, width: Int, height: Int) {
        val point = pointerOnScreen ?: return
        grab = Point(grabX, grabY)
        draggingSessionId = sessionId
        dropIndex = null
        state.openSessionInWindow(
            sessionId,
            x = point.x - grabX,
            y = point.y - grabY,
            width = width,
            height = height,
        )
    }

    /** Called by a detached window once it exists, so a drag in progress can move it. */
    fun attachWindow(sessionId: String, window: Window) {
        if (sessionId != draggingSessionId) return
        this.window = window
        pointerOnScreen?.let { window.setLocation(it.x - grab.x, it.y - grab.y) }
    }

    fun detachWindow(window: Window) {
        if (this.window === window) this.window = null
    }

    private fun follow(point: Point) {
        dropIndex = dropZone?.indexAt(point)
        window?.setLocation(point.x - grab.x, point.y - grab.y)
    }

    private fun finish(point: Point) {
        follow(point)
        val sessionId = draggingSessionId ?: return
        val index = dropIndex
        draggingSessionId = null
        dropIndex = null
        window = null
        if (index != null) state.returnToMainWindow(sessionId, index)
    }

    fun close() {
        Toolkit.getDefaultToolkit().removeAWTEventListener(listener)
    }
}

/**
 * Which pane slot a point over the split area's header strip falls into:
 * before the first pane whose centre lies right of it. With no panes open the
 * whole area accepts the drop.
 */
internal fun paneDropIndex(
    point: Point,
    area: Rectangle,
    stripHeight: Int,
    paneWidths: List<Float>,
    dividerWidth: Float,
): Int? {
    val zone = if (paneWidths.isEmpty()) area else Rectangle(area.x, area.y, area.width, stripHeight)
    if (!zone.contains(point)) return null
    val x = (point.x - area.x).toFloat()
    var left = 0f
    paneWidths.forEachIndexed { index, width ->
        if (x < left + width / 2f) return index
        left += width + dividerWidth
    }
    return paneWidths.size
}
