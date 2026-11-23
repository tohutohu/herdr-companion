package com.tohutohu.herdrcompanion.ui.projects

import android.text.format.DateUtils
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import com.tohutohu.herdrcompanion.AppContainer
import com.tohutohu.herdrcompanion.container
import com.tohutohu.herdrcompanion.data.api.GatewayEvent
import com.tohutohu.herdrcompanion.data.api.affectsSessionList
import com.tohutohu.herdrcompanion.data.api.pollWithEvents
import com.tohutohu.herdrcompanion.ui.resident.ResidentBackend
import com.tohutohu.herdrcompanion.ui.resident.ResidentSupport
import com.tohutohu.herdrcompanion.ui.resident.loadResidentHome
import com.tohutohu.herdrcompanion.ui.sessions.SessionListItemUiState
import com.tohutohu.herdrcompanion.ui.toUiModel
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

private const val POLL_MS = 5_000L
private const val CONNECTED_POLL_MS = 60_000L

/** What the project screens show: the groups, or why there are none. */
private class ProjectData(val state: ProjectsUiState, val backend: ResidentBackend?)

/**
 * Reads the projects, the session list and the resident tasks while a
 * project screen is visible: at once when the gateway reports a change, and
 * on a poll that slows down while its event stream is connected.
 */
@Composable
private fun rememberProjectData(): ProjectData {
    val container = LocalContext.current.container
    val backend = remember(container.settings.current.connectionId) { container.residentBackend() }
    val projects by container.projects.projects.collectAsState()
    val unsupported by container.projects.unsupported.collectAsState()
    val rows by container.sessions.collectAsState()
    val colors by container.sessionColors.colors.collectAsState(emptyMap())
    val resident by container.residentHome.collectAsState()
    var error by remember { mutableStateOf<String?>(null) }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    LaunchedEffect(lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            container.events.pollWithEvents(
                POLL_MS,
                CONNECTED_POLL_MS,
                { it.affectsSessionList() || it == GatewayEvent.ResidentChanged },
            ) { error = refreshAll(container, backend) }
        }
    }
    val sessions = rows.orEmpty().map { row ->
        SessionListItemUiState(
            session = row.toUiModel(),
            color = colors[row.id],
            relativeUpdatedAt = DateUtils.getRelativeTimeSpanString(row.updatedAt).toString(),
        )
    }
    val tasks = resident.tasks.takeIf { resident.support is ResidentSupport.Supported }
    val groups = projects?.let { groupByProject(it, sessions, tasks) }
    return ProjectData(
        ProjectsUiState(groups = groups, error = error, unsupported = unsupported),
        backend.takeIf { resident.support is ResidentSupport.Supported },
    )
}

/** Refreshes everything the project screens show; returns the error to show. */
private suspend fun refreshAll(container: AppContainer, backend: ResidentBackend): String? {
    var failure: String? = null
    suspend fun attempt(block: suspend () -> Unit) {
        try {
            block()
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            if (failure == null) failure = e.message ?: e.toString()
        }
    }
    attempt { container.projects.refresh() }
    attempt { container.repository.refreshSessions() }
    attempt { container.residentHome.value = loadResidentHome(backend, container.residentHome.value) }
    return failure
}

/** The Projects tab's home. */
@Composable
fun ProjectsHomeEntry(onOpen: (String) -> Unit) {
    val container = LocalContext.current.container
    val data = rememberProjectData()
    val scope = rememberCoroutineScope()
    ProjectListScreen(
        state = data.state,
        onOpen = onOpen,
        onRefresh = { scope.launch { refreshAll(container, container.residentBackend()) } },
    )
}

/** One project; [key] is its folder or "" for "その他". */
@Composable
fun ProjectEntry(
    key: String,
    onBack: () -> Unit,
    onOpenSession: (String) -> Unit,
    onOpenTask: (String) -> Unit,
    onNewSession: (String) -> Unit,
    onNewTask: (String) -> Unit,
) {
    val container = LocalContext.current.container
    val data = rememberProjectData()
    val scope = rememberCoroutineScope()
    var busy by remember { mutableStateOf(false) }
    var notifyError by remember { mutableStateOf<String?>(null) }
    val group = data.state.groups?.firstOrNull { it.key == key }
    ProjectDetailScreen(
        group = group,
        residentBackend = data.backend,
        notifyBusy = busy,
        error = notifyError ?: data.state.error.takeIf { group == null },
        onBack = onBack,
        onNotify = { level ->
            val path = group?.project?.path ?: return@ProjectDetailScreen
            scope.launch {
                busy = true
                notifyError = try {
                    container.projects.setNotify(path, level)
                    // The rows carry the project's level: read them again.
                    runCatching { container.repository.refreshSessions() }
                    runCatching { container.residentHome.value = loadResidentHome(container.residentBackend(), container.residentHome.value) }
                    null
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    "通知の設定を変えられませんでした: ${e.message}"
                } finally {
                    busy = false
                }
            }
        },
        onOpenSession = onOpenSession,
        onOpenTask = onOpenTask,
        onNewSession = onNewSession,
        onNewTask = onNewTask,
    )
}
