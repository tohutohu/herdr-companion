package com.tohutohu.herdrcompanion.desktop

import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.WindowPosition
import androidx.compose.ui.window.WindowPlacement
import com.tohutohu.herdrcompanion.data.AgentPresets
import com.tohutohu.herdrcompanion.data.DirectoryShortcuts
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.awt.Frame
import java.awt.GraphicsEnvironment
import java.awt.Rectangle
import java.awt.Window
import java.util.prefs.Preferences

private const val DEFAULT_WINDOW_WIDTH = 1280
private const val DEFAULT_WINDOW_HEIGHT = 800
private const val DEFAULT_SIDEBAR_WIDTH = 320f
private const val MIN_WINDOW_WIDTH = 880
private const val MIN_WINDOW_HEIGHT = 560
private const val MAX_WINDOW_WIDTH = 4_096
private const val MAX_WINDOW_HEIGHT = 4_096
private const val MIN_SIDEBAR_WIDTH = 240f
private const val MAX_SIDEBAR_WIDTH = 1_600f

/** The small, versionable part of the native window state we persist. */
data class DesktopWindowSnapshot(
    val width: Int = DEFAULT_WINDOW_WIDTH,
    val height: Int = DEFAULT_WINDOW_HEIGHT,
    val x: Int? = null,
    val y: Int? = null,
    val maximized: Boolean = false,
) {
    fun normalized(): DesktopWindowSnapshot = copy(
        width = width.coerceIn(MIN_WINDOW_WIDTH, MAX_WINDOW_WIDTH),
        height = height.coerceIn(MIN_WINDOW_HEIGHT, MAX_WINDOW_HEIGHT),
    )

    fun encode(): String = listOf(
        width,
        height,
        x ?: "",
        y ?: "",
        maximized,
    ).joinToString(",")

    companion object {
        fun decode(value: String?): DesktopWindowSnapshot? {
            val fields = value?.split(',') ?: return null
            if (fields.size != 5) return null
            return runCatching {
                DesktopWindowSnapshot(
                    width = fields[0].toInt(),
                    height = fields[1].toInt(),
                    x = fields[2].toIntOrNull(),
                    y = fields[3].toIntOrNull(),
                    maximized = fields[4].toBooleanStrict(),
                ).normalized()
            }.getOrNull()
        }
    }
}

/** Pure visibility check kept separate so restore safety can be unit tested. */
internal fun windowIntersectsAnyScreen(snapshot: DesktopWindowSnapshot, screens: List<Rectangle>): Boolean {
    val x = snapshot.x ?: return false
    val y = snapshot.y ?: return false
    val bounds = Rectangle(x, y, snapshot.width, snapshot.height)
    return screens.any { it.intersects(bounds) }
}

object DesktopPreferences {
    private const val NODE = "com.tohutohu.herdrcompanion.desktop"
    private const val WINDOW_KEY = "window"
    private const val SIDEBAR_KEY = "sidebarWidth"
    private const val NOTIFICATIONS_KEY = "notificationsEnabled"
    private const val DIRECTORY_FAVORITES_KEY = "directoryFavorites"
    private const val DIRECTORY_RECENTS_KEY = "directoryRecents"
    private const val AGENT_PRESETS_KEY = "agentPresets"
    private const val LAYOUT_KEY = "sessionLayout"

    private val store: Preferences
        get() = Preferences.userRoot().node(NODE)

    fun loadWindow(): DesktopWindowSnapshot {
        val stored = DesktopWindowSnapshot.decode(store.get(WINDOW_KEY, null)) ?: DesktopWindowSnapshot()
        val safePosition = if (stored.x == null || stored.y == null) {
            stored.copy(x = null, y = null, maximized = false)
        } else {
            val screens = runCatching {
                GraphicsEnvironment.getLocalGraphicsEnvironment().screenDevices.map { device ->
                    device.defaultConfiguration.bounds
                }
            }.getOrDefault(emptyList())
            if (screens.isEmpty() || windowIntersectsAnyScreen(stored, screens)) stored
            else stored.copy(x = null, y = null, maximized = false)
        }
        return safePosition.normalized()
    }

    fun saveWindow(window: Window) {
        val maximized = (window as? Frame)?.let { frame ->
            frame.extendedState and Frame.MAXIMIZED_BOTH == Frame.MAXIMIZED_BOTH
        } ?: false
        DesktopWindowSnapshot(
            width = window.width,
            height = window.height,
            x = window.x,
            y = window.y,
            maximized = maximized,
        ).normalized().let { store.put(WINDOW_KEY, it.encode()) }
    }

    fun loadSidebarWidth(): Float = store.getFloat(SIDEBAR_KEY, DEFAULT_SIDEBAR_WIDTH)
        .coerceIn(MIN_SIDEBAR_WIDTH, MAX_SIDEBAR_WIDTH)

    fun saveSidebarWidth(width: Float) {
        store.putFloat(SIDEBAR_KEY, width.coerceIn(MIN_SIDEBAR_WIDTH, MAX_SIDEBAR_WIDTH))
    }

    /** New-session shortcuts are local Desktop preferences, not Gateway data. */
    fun loadDirectoryShortcuts(): DirectoryShortcuts = DirectoryShortcuts(
        favorites = decodeLines(store.get(DIRECTORY_FAVORITES_KEY, null)),
        recents = decodeLines(store.get(DIRECTORY_RECENTS_KEY, null)),
    )

    fun saveDirectoryShortcuts(shortcuts: DirectoryShortcuts) {
        store.put(DIRECTORY_FAVORITES_KEY, encodeLines(shortcuts.favorites))
        store.put(DIRECTORY_RECENTS_KEY, encodeLines(shortcuts.recents))
    }

    fun loadAgentPresets(): AgentPresets = decodeAgentPresets(store.get(AGENT_PRESETS_KEY, null))

    fun saveAgentPresets(presets: AgentPresets) {
        store.put(AGENT_PRESETS_KEY, encodeAgentPresets(presets))
    }

    fun loadLayout(): DesktopLayoutSnapshot = decodeLayout(store.get(LAYOUT_KEY, null))

    fun saveLayout(layout: DesktopLayoutSnapshot) {
        store.put(LAYOUT_KEY, encodeLayout(layout))
    }

    var notificationsEnabled: Boolean
        get() = store.getBoolean(NOTIFICATIONS_KEY, true)
        set(value) { store.putBoolean(NOTIFICATIONS_KEY, value) }

    fun clearWindow() {
        store.remove(WINDOW_KEY)
    }
}

/** Newline is sufficient here: the values are absolute filesystem paths. */
internal fun encodeLines(values: List<String>): String =
    values.filter { it.isNotEmpty() }.distinct().joinToString("\n")

internal fun decodeLines(value: String?): List<String> =
    value.orEmpty().split('\n').filter { it.isNotEmpty() }.distinct()

internal fun decodeAgentPresets(value: String?): AgentPresets =
    value?.let { runCatching { json.decodeFromString<AgentPresets>(it) }.getOrNull() } ?: AgentPresets()

internal fun encodeAgentPresets(value: AgentPresets): String = json.encodeToString(value)

/** Open panes, their order and widths, restored on the next launch. */
@Serializable
data class DesktopLayoutSnapshot(
    val openSessionIds: List<String> = emptyList(),
    val focusedSessionId: String? = null,
    val paneWeights: List<Float> = emptyList(),
) {
    fun normalized(): DesktopLayoutSnapshot {
        val ids = openSessionIds.filter { it.isNotEmpty() }.distinct()
        return DesktopLayoutSnapshot(
            openSessionIds = ids,
            focusedSessionId = focusedSessionId?.takeIf { it in ids } ?: ids.lastOrNull(),
            paneWeights = paneWeights
                .takeIf { weights -> weights.size == ids.size && weights.all { it.isFinite() && it > 0f } }
                ?: equalPaneWeights(ids.size),
        )
    }
}

internal fun decodeLayout(value: String?): DesktopLayoutSnapshot =
    (value?.let { runCatching { json.decodeFromString<DesktopLayoutSnapshot>(it) }.getOrNull() } ?: DesktopLayoutSnapshot())
        .normalized()

internal fun encodeLayout(value: DesktopLayoutSnapshot): String = json.encodeToString(value.normalized())

private val json = Json { ignoreUnknownKeys = true }

fun DesktopWindowSnapshot.position(): WindowPosition = if (x != null && y != null) {
    WindowPosition.Absolute(x.dp, y.dp)
} else {
    WindowPosition.PlatformDefault
}

fun DesktopWindowSnapshot.placement(): WindowPlacement =
    if (maximized) WindowPlacement.Maximized else WindowPlacement.Floating
