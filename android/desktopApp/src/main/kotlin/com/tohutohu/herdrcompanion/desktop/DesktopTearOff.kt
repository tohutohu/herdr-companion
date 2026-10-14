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
import kotlin.math.floor

/** The main window's pane header strip, where a dragged-out session can be dropped back in. */
fun interface DesktopPaneDropZone {
    /**
     * How a session held at [point] (screen points) would dock, or null
     * outside the strip. [grabFraction] is where across its width the pointer
     * holds the session.
     */
    fun previewAt(point: Point, grabFraction: Float): DesktopDockPreview?
}

/**
 * A dragged-out session shown back among the panes: the slot it takes if
 * released now, and how far, in points, it sits from that slot while
 * following the pointer.
 */
data class DesktopDockPreview(val index: Int, val offset: Float)

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

    /** Where the dragged session would dock if released now; null while it is outside the drop zone. */
    var dockPreview by mutableStateOf<DesktopDockPreview?>(null)
        private set

    var dropZone: DesktopPaneDropZone? = null

    private var grab = Point()
    private var grabFraction = 0.5f
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
        grabFraction = if (width > 0) grabX.toFloat() / width else 0.5f
        draggingSessionId = sessionId
        dockPreview = null
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
        dockPreview = dropZone?.previewAt(point, grabFraction)
        window?.setLocation(point.x - grab.x, point.y - grab.y)
    }

    private fun finish(point: Point) {
        follow(point)
        val sessionId = draggingSessionId ?: return
        val index = dockPreview?.index
        draggingSessionId = null
        dockPreview = null
        window = null
        if (index != null) state.returnToMainWindow(sessionId, index)
    }

    fun close() {
        Toolkit.getDefaultToolkit().removeAWTEventListener(listener)
    }
}

/**
 * Where a session held at [point] would dock among [paneCount] open panes.
 * Docking resets the panes to equal widths, so the preview lays them out that
 * way too and picks the slot nearest to where the pointer holds the session.
 * With no panes open the whole area accepts the drop; otherwise only the
 * header strip at its top.
 */
internal fun dockPreview(
    point: Point,
    area: Rectangle,
    stripHeight: Int,
    paneCount: Int,
    dividerWidth: Float,
    grabFraction: Float,
): DesktopDockPreview? {
    val zone = if (paneCount == 0) area else Rectangle(area.x, area.y, area.width, stripHeight)
    if (!zone.contains(point)) return null
    val slotWidth = (area.width - dividerWidth * paneCount) / (paneCount + 1)
    if (slotWidth <= 0f) return DesktopDockPreview(0, 0f)
    val left = (point.x - area.x) - grabFraction.coerceIn(0f, 1f) * slotWidth
    val index = floor((left + slotWidth / 2f) / (slotWidth + dividerWidth)).toInt().coerceIn(0, paneCount)
    val slotLeft = index * (slotWidth + dividerWidth)
    val clampedLeft = left.coerceIn(0f, (area.width - slotWidth).coerceAtLeast(0f))
    return DesktopDockPreview(index, clampedLeft - slotLeft)
}
