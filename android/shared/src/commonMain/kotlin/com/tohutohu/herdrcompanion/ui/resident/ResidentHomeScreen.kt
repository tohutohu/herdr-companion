package com.tohutohu.herdrcompanion.ui.resident

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Badge
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.data.api.ResidentTaskDto
import com.tohutohu.herdrcompanion.data.api.TaskState
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/** Whether the connected gateway offers resident tasks. */
sealed interface ResidentSupport {
    data object Checking : ResidentSupport
    data class Supported(val defaultTimezone: String) : ResidentSupport
    /** The gateway answered but has no resident tasks: it is older than the app. */
    data object Unsupported : ResidentSupport
    /** The gateway could not be asked; [message] says why. */
    data class Unknown(val message: String) : ResidentSupport
}

/** State of the task list, kept per connection. */
data class ResidentHomeState(
    val support: ResidentSupport = ResidentSupport.Checking,
    /** Null until the first successful load: never "no tasks" because of an error. */
    val tasks: List<ResidentTaskDto>? = null,
    val error: String? = null,
    val refreshing: Boolean = false,
)

const val RESIDENT_LIST_POLL_MS = 5_000L

/** Checks support and loads the task list; returns the new state. */
suspend fun loadResidentHome(backend: ResidentBackend, current: ResidentHomeState): ResidentHomeState {
    var support = current.support
    if (support !is ResidentSupport.Supported) {
        support = try {
            val caps = backend.capabilities()
            caps.resident?.let { ResidentSupport.Supported(it.defaultTimezone) } ?: ResidentSupport.Unsupported
        } catch (e: CancellationException) {
            throw e
        } catch (e: ResidentApiException) {
            return current.copy(support = ResidentSupport.Unknown(e.message ?: "接続できません"), error = e.message, refreshing = false)
        }
    }
    if (support !is ResidentSupport.Supported) return current.copy(support = support, error = null, refreshing = false)
    return try {
        current.copy(support = support, tasks = backend.tasks(), error = null, refreshing = false)
    } catch (e: CancellationException) {
        throw e
    } catch (e: ResidentApiException) {
        if (e.notFound) current.copy(support = ResidentSupport.Unsupported, refreshing = false)
        else current.copy(support = support, error = e.message, refreshing = false)
    }
}

/**
 * The 常駐 tab's home: every task with its state, next run, last success,
 * latest important report and whether it waits for the user. It polls while
 * shown. [state] lives with the caller so switching tabs keeps it.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ResidentHomeScreen(
    backend: ResidentBackend,
    state: ResidentHomeState,
    onState: (ResidentHomeState) -> Unit,
    onOpenTask: (String) -> Unit,
    onCreate: () -> Unit,
    modifier: Modifier = Modifier,
    showTopBar: Boolean = true,
    selectedTaskId: String? = null,
    actions: @Composable () -> Unit = {},
) {
    val scope = rememberCoroutineScope()
    var latest by remember { mutableStateOf(state) }
    latest = state
    var reload by remember { mutableIntStateOf(0) }
    LaunchedEffect(backend.connectionKey, reload) {
        while (true) {
            onState(loadResidentHome(backend, latest))
            delay(RESIDENT_LIST_POLL_MS)
        }
    }
    Scaffold(
        modifier = modifier,
        topBar = {
            if (showTopBar) {
                TopAppBar(
                    title = { Text("常駐") },
                    actions = {
                        IconButton(onClick = { onState(state.copy(refreshing = true)); reload++ }) {
                            Icon(Icons.Default.Refresh, contentDescription = "再読み込み")
                        }
                        actions()
                    },
                )
            }
        },
        floatingActionButton = {
            if (state.support is ResidentSupport.Supported) {
                ExtendedFloatingActionButton(
                    onClick = onCreate,
                    modifier = Modifier.semantics { contentDescription = "新しいタスク" },
                    icon = { Icon(Icons.Default.Add, contentDescription = null) },
                    text = { Text("新しいタスク") },
                )
            }
        },
    ) { padding ->
        Box(Modifier.fillMaxSize().padding(padding)) {
            val tasks = state.tasks
            when {
                state.support == ResidentSupport.Unsupported -> Notice(
                    "このGatewayは常駐タスクに対応していません",
                    "Mac の Herdr Companion Gateway を更新すると使えるようになります。",
                )
                tasks == null && state.support is ResidentSupport.Unknown -> Notice(
                    "Gatewayに接続できません",
                    (state.support as ResidentSupport.Unknown).message,
                    action = "再試行" to { reload++ },
                )
                tasks == null -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
                else -> Column(Modifier.fillMaxSize()) {
                    state.error?.let { ErrorBanner("最新の状態を取得できません（表示は前回の内容）: $it") { scope.launch { reload++ } } }
                    if (tasks.isEmpty()) {
                        Notice(
                            "常駐タスクはまだありません",
                            "「毎朝このリポジトリを確認して、対応が必要なら知らせて」のように、継続して任せたいことを登録します。",
                            action = "タスクを作成" to onCreate,
                        )
                    } else {
                        LazyColumn(contentPadding = PaddingValues(bottom = 96.dp)) {
                            items(tasks, key = { it.id }) { task ->
                                ResidentTaskRow(task, backend, selected = task.id == selectedTaskId, onClick = { onOpenTask(task.id) })
                                HorizontalDivider()
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun ResidentTaskRow(task: ResidentTaskDto, backend: ResidentBackend, selected: Boolean, onClick: () -> Unit) {
    val style = taskStateStyle(task.state)
    val needsYou = task.state == TaskState.WAITING_USER || task.state == TaskState.NEEDS_ATTENTION
    Surface(
        color = if (selected) MaterialTheme.colorScheme.secondaryContainer else MaterialTheme.colorScheme.surface,
        modifier = Modifier.fillMaxWidth().clickable(onClick = onClick).semantics { contentDescription = "常駐タスク ${task.name}" },
    ) {
        Column(Modifier.padding(horizontal = 16.dp, vertical = 12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    task.name,
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = if (task.unread > 0 || needsYou) FontWeight.Bold else FontWeight.Normal,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
                if (task.unread > 0) {
                    Badge { Text("${task.unread}") }
                    Spacer(Modifier.width(8.dp))
                }
                Text(style.label, color = style.color, style = MaterialTheme.typography.labelMedium)
            }
            val facts = buildList {
                if (!task.enabled) add("一時停止中")
                else task.nextRunAt?.let { add("次回 ${backend.formatInstant(it)}") } ?: add("自動実行なし")
                task.lastSuccessAt?.let { add("最終成功 ${backend.relative(it)}") }
                add(providerLabel(task.provider))
            }
            Text(facts.joinToString(" · "), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            task.pendingQuestion?.let {
                Text("判断待ち: ${it.text}", style = MaterialTheme.typography.bodyMedium, color = taskStateStyle(TaskState.WAITING_USER).color, maxLines = 2, overflow = TextOverflow.Ellipsis)
            } ?: task.attention?.let {
                Text(it, style = MaterialTheme.typography.bodyMedium, color = taskStateStyle(TaskState.NEEDS_ATTENTION).color, maxLines = 2, overflow = TextOverflow.Ellipsis)
            }
            task.latestReport?.let {
                Text("${backend.relative(it.at)}: ${it.summary}", style = MaterialTheme.typography.bodyMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
            }
        }
    }
}

@Composable
internal fun Notice(title: String, body: String, action: Pair<String, () -> Unit>? = null) {
    Column(
        Modifier.fillMaxSize().padding(32.dp),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(title, style = MaterialTheme.typography.titleMedium)
        Spacer(Modifier.padding(4.dp))
        Text(body, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        action?.let { (label, onClick) ->
            Spacer(Modifier.padding(8.dp))
            Button(onClick = onClick) { Text(label) }
        }
    }
}

@Composable
internal fun ErrorBanner(text: String, onRetry: (() -> Unit)? = null) {
    Card(
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.errorContainer),
        modifier = Modifier.fillMaxWidth().padding(8.dp),
    ) {
        Row(Modifier.padding(12.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(text, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onErrorContainer, modifier = Modifier.weight(1f))
            onRetry?.let { OutlinedButton(onClick = it) { Text("再試行") } }
        }
    }
}
