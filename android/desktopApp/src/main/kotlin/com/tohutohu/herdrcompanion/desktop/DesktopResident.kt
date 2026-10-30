package com.tohutohu.herdrcompanion.desktop

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.width
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.VerticalDivider
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.data.Settings
import com.tohutohu.herdrcompanion.data.api.GatewayApi
import com.tohutohu.herdrcompanion.data.api.ResidentGatewayBackend
import com.tohutohu.herdrcompanion.data.api.ResidentNotificationDto
import com.tohutohu.herdrcompanion.ui.resident.ResidentBackend
import com.tohutohu.herdrcompanion.ui.resident.ResidentHomeScreen
import com.tohutohu.herdrcompanion.ui.resident.ResidentHomeState
import com.tohutohu.herdrcompanion.ui.resident.ResidentSupport
import com.tohutohu.herdrcompanion.ui.resident.ResidentTaskForm
import com.tohutohu.herdrcompanion.ui.resident.ResidentTaskScreen
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import okhttp3.OkHttpClient
import java.security.MessageDigest
import java.time.Instant

/** The main window's top-level views. */
enum class DesktopTopTab { SESSIONS, RESIDENT }

/** What the 常駐 detail pane shows. */
sealed interface ResidentPane {
    data object None : ResidentPane
    data class Task(val id: String) : ResidentPane
    data class Edit(val id: String?) : ResidentPane
}

/** A resident notification the user can jump to from inside the app. */
data class ResidentBanner(val notificationId: String, val taskId: String, val title: String, val body: String)

/**
 * The Mac UI's resident (常駐) state: kept apart from the session panes, so
 * switching between the views keeps both. Tasks of a different gateway are
 * never shown: a new connection gets a new backend and an empty state.
 *
 * Notifications: the gateway decides (conditions, keys already sent,
 * cooldown, quiet hours) and keeps an outbox; this polls it and shows the
 * new entries, as Android shows the pushed ones.
 */
class DesktopResidentState(
    private val settings: () -> Settings,
    private val http: OkHttpClient,
    private val now: () -> Instant = Instant::now,
) {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private var cached: Pair<String, ResidentGatewayBackend>? = null
    private var pollJob: Job? = null
    /** Notification ids already handled; null until the first read (which shows nothing). */
    private var seen: Set<String>? = null

    var topTab by mutableStateOf(DesktopTopTab.SESSIONS)
    var home by mutableStateOf(ResidentHomeState())
    var pane by mutableStateOf<ResidentPane>(ResidentPane.None)
    var banner by mutableStateOf<ResidentBanner?>(null)
        private set
    /** A notification to show as a system notification. */
    var notification by mutableStateOf<DesktopNotificationEvent?>(null)
        private set

    val unread: Int get() = home.tasks.orEmpty().sumOf { it.unread }
    val waiting: Int get() = home.tasks.orEmpty().count { it.pendingQuestion != null || it.state == "needs_attention" }

    @Synchronized
    fun backend(): ResidentBackend {
        val s = settings()
        val key = connectionKey(s)
        cached?.takeIf { it.first == key }?.let { return it.second }
        home = ResidentHomeState()
        pane = ResidentPane.None
        banner = null
        seen = null
        val pinned = s
        return ResidentGatewayBackend(GatewayApi(http) { pinned }, key).also { cached = key to it }
    }

    fun start() {
        if (pollJob != null) return
        pollJob = scope.launch {
            while (isActive) {
                poll()
                delay(15_000)
            }
        }
    }

    private suspend fun poll() {
        val s = settings()
        if (!s.isConfigured) return
        backend()
        val api = GatewayApi(http) { s }
        val entries = try {
            if (api.capabilities().resident == null) return
            api.residentNotifications()
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            return
        }
        val due = entries.filter { runCatching { !Instant.parse(it.notBefore).isAfter(now()) }.getOrDefault(true) }
        val known = seen
        seen = (known.orEmpty() + due.map { it.id }).toSet()
        if (known == null) return
        due.filter { it.id !in known }.lastOrNull()?.let(::announce)
    }

    private fun announce(n: ResidentNotificationDto) {
        banner = ResidentBanner(n.id, n.taskId, n.title, n.body)
        notification = DesktopNotificationEvent(id = "resident:${n.id}", title = "常駐: ${n.title}", body = n.body)
    }

    fun dismissNotification(id: String) {
        if (notification?.id == id) notification = null
    }

    fun dismissBanner() {
        banner = null
    }

    /** Shows a task in the 常駐 view (from the banner or a menu). */
    fun openTask(id: String) {
        topTab = DesktopTopTab.RESIDENT
        pane = ResidentPane.Task(id)
        if (banner?.taskId == id) banner = null
    }

    fun close() = scope.cancel()

    companion object {
        fun connectionKey(s: Settings): String {
            val digest = MessageDigest.getInstance("SHA-256").digest((s.gatewayUrl.trim() + "\n" + s.token.trim()).toByteArray())
            return digest.take(12).joinToString("") { "%02x".format(it) }
        }
    }
}

/**
 * The 常駐 view: the task list beside the selected task or the form. A run
 * continued as a session is handed to [onOpenSession].
 */
@Composable
fun DesktopResidentView(state: DesktopResidentState, onOpenSession: (String) -> Unit, modifier: Modifier = Modifier) {
    val backend = state.backend()
    val tz = (state.home.support as? ResidentSupport.Supported)?.defaultTimezone ?: "Asia/Tokyo"
    Row(modifier.fillMaxSize()) {
        ResidentHomeScreen(
            backend = backend,
            state = state.home,
            onState = { state.home = it },
            onOpenTask = { state.pane = ResidentPane.Task(it) },
            onCreate = { state.pane = ResidentPane.Edit(null) },
            modifier = Modifier.width(380.dp).fillMaxHeight(),
            selectedTaskId = (state.pane as? ResidentPane.Task)?.id,
        )
        VerticalDivider()
        Box(Modifier.weight(1f).fillMaxHeight()) {
            when (val pane = state.pane) {
                ResidentPane.None -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    Text("タスクを選ぶか、新しいタスクを作成してください", color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                is ResidentPane.Task -> key(backend.connectionKey, pane.id) {
                    ResidentTaskScreen(
                        backend = backend,
                        taskId = pane.id,
                        onBack = null,
                        onEdit = { state.pane = ResidentPane.Edit(it) },
                        onDeleted = { state.pane = ResidentPane.None },
                        onOpenSession = onOpenSession,
                    )
                }
                is ResidentPane.Edit -> key(backend.connectionKey, pane.id) {
                    ResidentTaskForm(
                        backend = backend,
                        existingId = pane.id,
                        defaultTimezone = tz,
                        onSaved = { state.pane = ResidentPane.Task(it) },
                        onCancel = { state.pane = pane.id?.let { ResidentPane.Task(it) } ?: ResidentPane.None },
                    )
                }
            }
        }
    }
}
