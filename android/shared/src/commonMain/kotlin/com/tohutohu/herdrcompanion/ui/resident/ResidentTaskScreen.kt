package com.tohutohu.herdrcompanion.ui.resident

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.AssistChip
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.PrimaryTabRow
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Tab
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.data.api.MemoryUpdateRequest
import com.tohutohu.herdrcompanion.data.api.ResidentMessageDto
import com.tohutohu.herdrcompanion.data.api.ResidentRunDto
import com.tohutohu.herdrcompanion.data.api.ResidentTaskDetailDto
import com.tohutohu.herdrcompanion.data.api.RunStatus
import com.tohutohu.herdrcompanion.ui.markdown.MarkdownText
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject

private val prettyJson = Json { prettyPrint = true }

/** One task's data as the detail screen last loaded it. */
data class ResidentTaskState(
    val detail: ResidentTaskDetailDto? = null,
    val messages: List<ResidentMessageDto> = emptyList(),
    val runs: List<ResidentRunDto> = emptyList(),
    val error: String? = null,
    val gone: Boolean = false,
)

private suspend fun loadTask(backend: ResidentBackend, id: String, current: ResidentTaskState): ResidentTaskState = try {
    val detail = backend.task(id)
    val messages = backend.messages(id).messages
    val runs = backend.runs(id).runs
    ResidentTaskState(detail, messages, runs)
} catch (e: CancellationException) {
    throw e
} catch (e: ResidentApiException) {
    if (e.notFound) current.copy(gone = true, error = e.message) else current.copy(error = e.message)
}

/**
 * One resident task: its state and next runs, the open question, the
 * conversation with follow-up instructions, the run history, the memory and
 * the settings. Polls while shown, faster while a run is queued or running.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ResidentTaskScreen(
    backend: ResidentBackend,
    taskId: String,
    onBack: (() -> Unit)?,
    onEdit: (String) -> Unit,
    onDeleted: () -> Unit,
    modifier: Modifier = Modifier,
    /** Opened from a notification: show the conversation first. */
    initialTab: Int = 0,
) {
    var state by remember(backend.connectionKey, taskId) { mutableStateOf(ResidentTaskState()) }
    var reload by remember { mutableIntStateOf(0) }
    var busy by remember { mutableStateOf<String?>(null) }
    var actionError by remember { mutableStateOf<String?>(null) }
    var tab by rememberSaveable(taskId) { mutableIntStateOf(initialTab) }
    val scope = rememberCoroutineScope()

    LaunchedEffect(backend.connectionKey, taskId, reload) {
        while (true) {
            state = loadTask(backend, taskId, state)
            val active = state.runs.any { it.status == RunStatus.RUNNING || it.status == RunStatus.QUEUED }
            delay(if (active) 2_500L else 8_000L)
        }
    }
    // Reading the conversation marks the task's reports read.
    val unread = state.detail?.task?.unread ?: 0
    LaunchedEffect(taskId, tab, unread) {
        if (tab == 0 && unread > 0) runCatching { backend.markRead(taskId) }
    }

    fun act(label: String, onSuccess: () -> Unit = {}, block: suspend () -> Unit) {
        if (busy != null) return
        busy = label
        actionError = null
        scope.launch {
            try {
                block()
                onSuccess()
                reload++
            } catch (e: CancellationException) {
                throw e
            } catch (e: ResidentApiException) {
                actionError = "$label できませんでした: ${e.message}"
            } finally {
                busy = null
            }
        }
    }

    var menu by remember { mutableStateOf(false) }
    var confirmDelete by remember { mutableStateOf(false) }
    val task = state.detail?.task
    Scaffold(
        modifier = modifier,
        topBar = {
            TopAppBar(
                title = { Text(task?.name ?: "常駐タスク") },
                navigationIcon = {
                    onBack?.let { IconButton(onClick = it) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "戻る") } }
                },
                actions = {
                    if (task != null) {
                        TextButton(onClick = { onEdit(taskId) }) { Text("設定を編集") }
                        IconButton(onClick = { menu = true }) { Icon(Icons.Default.MoreVert, contentDescription = "その他") }
                        DropdownMenu(menu, onDismissRequest = { menu = false }) {
                            DropdownMenuItem(
                                text = { Text(if (task.enabled) "一時停止" else "再開") },
                                onClick = {
                                    menu = false
                                    act(if (task.enabled) "一時停止" else "再開") { backend.setEnabled(taskId, !task.enabled) }
                                },
                            )
                            DropdownMenuItem(text = { Text("削除") }, onClick = { menu = false; confirmDelete = true })
                        }
                    }
                },
            )
        },
    ) { padding ->
        Column(Modifier.fillMaxSize().padding(padding).imePadding()) {
            when {
                state.gone -> Notice("このタスクは削除されています", state.error.orEmpty())
                state.detail == null && state.error != null -> Notice("読み込めません", state.error.orEmpty(), action = "再試行" to { reload++ })
                state.detail == null -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
                else -> {
                    val detail = state.detail!!
                    state.error?.let { ErrorBanner("最新の状態を取得できません（表示は前回の内容）: $it") { reload++ } }
                    actionError?.let { ErrorBanner(it) }
                    TaskHeader(detail, state.runs, backend, busy, onRun = {
                        val requestId = backend.newRequestId()
                        act("今すぐ実行") { backend.runNow(taskId, requestId) }
                    }, onStop = { runId -> act("実行停止") { backend.cancelRun(runId) } }, onResume = {
                        act("再開") { backend.setEnabled(taskId, true) }
                    })
                    detail.task.pendingQuestion?.let { q ->
                        QuestionCard(q.text, q.options, backend.formatInstant(q.expiresAt), busy != null) { answer ->
                            val requestId = backend.newRequestId()
                            act("回答") { backend.answer(q.id, answer, requestId) }
                        }
                    }
                    PrimaryTabRow(selectedTabIndex = tab) {
                        listOf("会話", "実行履歴", "記憶", "設定").forEachIndexed { i, title ->
                            Tab(selected = tab == i, onClick = { tab = i }, text = { Text(title) })
                        }
                    }
                    when (tab) {
                        0 -> Conversation(state.messages, backend, Modifier.weight(1f), sending = busy == "追加指示") { text, requestId, sent ->
                            act("追加指示", onSuccess = sent) { backend.postMessage(taskId, text, requestId) }
                        }
                        1 -> RunHistory(state.runs, backend, Modifier.weight(1f), busy != null,
                            onCancel = { act("実行停止") { backend.cancelRun(it) } },
                            onDismiss = { act("確認済みにする") { backend.dismissRun(it) } })
                        2 -> MemoryEditor(detail, Modifier.weight(1f), busy != null) { summary, stateText, version ->
                            act("記憶の保存") {
                                val obj = if (stateText.isBlank()) JsonObject(emptyMap()) else Json.parseToJsonElement(stateText) as? JsonObject
                                    ?: throw ResidentApiException(400, "invalid", "状態は JSON オブジェクトで書いてください")
                                backend.putMemory(taskId, MemoryUpdateRequest(version, summary, obj as Map<String, JsonElement>))
                            }
                        }
                        else -> SettingsSummary(detail, backend, Modifier.weight(1f)) { onEdit(taskId) }
                    }
                }
            }
        }
    }
    if (confirmDelete) {
        AlertDialog(
            onDismissRequest = { confirmDelete = false },
            title = { Text("タスクを削除しますか？") },
            text = { Text("以後は実行されません。実行中の Run は停止します。履歴は Gateway に残りますが、アプリからは見られなくなります。") },
            confirmButton = {
                TextButton(onClick = {
                    confirmDelete = false
                    scope.launch {
                        try {
                            backend.delete(taskId)
                            onDeleted()
                        } catch (e: ResidentApiException) {
                            actionError = "削除できませんでした: ${e.message}"
                        }
                    }
                }) { Text("削除") }
            },
            dismissButton = { TextButton(onClick = { confirmDelete = false }) { Text("キャンセル") } },
        )
    }
}

@Composable
private fun TaskHeader(
    detail: ResidentTaskDetailDto,
    runs: List<ResidentRunDto>,
    backend: ResidentBackend,
    busy: String?,
    onRun: () -> Unit,
    onStop: (String) -> Unit,
    onResume: () -> Unit,
) {
    val task = detail.task
    val style = taskStateStyle(task.state)
    val running = runs.firstOrNull { it.status == RunStatus.RUNNING }
    val queued = runs.firstOrNull { it.status == RunStatus.QUEUED }
    Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(style.label, color = style.color, style = MaterialTheme.typography.titleSmall)
            Text(
                "  ${scheduleLabel(task.schedule, backend::formatInstant)} · ${providerLabel(task.provider)} ${task.model ?: "既定モデル"}${task.effort?.let { " / $it" }.orEmpty()}",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        val next = detail.upcoming.take(3).map(backend::formatInstant)
        val self = detail.wakeups.firstOrNull { it.kind == "self" }
        Text(
            buildString {
                append(if (!task.enabled) "一時停止中（自動実行しません）" else if (next.isEmpty()) "予定された自動実行はありません" else "次回: ${next.joinToString(", ")}")
                self?.let { append("\n再確認の予約: ${backend.formatInstant(it.dueAt)}${it.reason?.let { r -> "（$r）" }.orEmpty()}") }
                task.lastSuccessAt?.let { append("\n最終成功: ${backend.formatInstant(it)}") }
                append("\n今日の起動: ${task.runsToday} / ${task.limits.maxRunsPerDay} 回")
            },
            style = MaterialTheme.typography.bodySmall,
        )
        task.attention?.let { Text(it, color = taskStateStyle("needs_attention").color, style = MaterialTheme.typography.bodySmall) }
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            if (running != null) {
                OutlinedButton(onClick = { onStop(running.id) }, enabled = busy == null) { Text("今回の実行を停止") }
            } else {
                Button(onClick = onRun, enabled = busy == null && queued == null) { Text(if (queued != null) "実行待ち" else "今すぐ実行") }
            }
            if (!task.enabled) FilledTonalButton(onClick = onResume, enabled = busy == null) { Text("再開") }
            busy?.let { Text("$it…", style = MaterialTheme.typography.bodySmall, modifier = Modifier.align(Alignment.CenterVertically)) }
        }
    }
    HorizontalDivider()
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun QuestionCard(text: String, options: List<String>, expires: String, busy: Boolean, onAnswer: (String) -> Unit) {
    var answer by rememberSaveable(text) { mutableStateOf("") }
    Card(
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.tertiaryContainer),
        modifier = Modifier.fillMaxWidth().padding(8.dp),
    ) {
        Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Text("判断待ち", style = MaterialTheme.typography.labelLarge)
            MarkdownText(text)
            if (options.isNotEmpty()) {
                FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    options.forEach { o -> AssistChip(onClick = { answer = o }, label = { Text(o) }) }
                }
            }
            OutlinedTextField(answer, { answer = it }, label = { Text("回答") }, modifier = Modifier.fillMaxWidth())
            Text("回答は情報として渡されます。送信・購入などの許可にはなりません（権限は設定で変更）。期限: $expires",
                style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Button(onClick = { onAnswer(answer.trim()) }, enabled = !busy && answer.isNotBlank()) { Text("回答して続ける") }
        }
    }
}

@Composable
private fun Conversation(
    messages: List<ResidentMessageDto>,
    backend: ResidentBackend,
    modifier: Modifier,
    sending: Boolean,
    onSend: (text: String, requestId: String, sent: () -> Unit) -> Unit,
) {
    val list = rememberLazyListState()
    LaunchedEffect(messages.size) { if (messages.isNotEmpty()) list.scrollToItem(messages.size - 1) }
    var draft by rememberSaveable { mutableStateOf("") }
    // One request id per logical send, reused when the same text is retried
    // after a failure, so a lost answer does not queue the instruction twice.
    var requestId by remember { mutableStateOf<Pair<String, String>?>(null) }
    Column(modifier) {
        if (messages.isEmpty()) {
            Box(Modifier.weight(1f).fillMaxWidth(), contentAlignment = Alignment.Center) {
                Text("報告はまだありません。変化のない定期実行は会話に出さず、実行履歴にだけ残ります。",
                    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(24.dp))
            }
        } else {
            LazyColumn(Modifier.weight(1f), state = list, contentPadding = PaddingValues(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                items(messages, key = { it.id }) { m -> MessageBubble(m, backend) }
            }
        }
        HorizontalDivider()
        Row(Modifier.fillMaxWidth().padding(8.dp), verticalAlignment = Alignment.CenterVertically) {
            OutlinedTextField(
                draft, { draft = it },
                placeholder = { Text("追加指示（例: 詳しく調べて）") },
                modifier = Modifier.weight(1f).heightIn(max = 160.dp),
            )
            IconButton(
                onClick = {
                    val text = draft.trim()
                    val id = requestId?.takeIf { it.first == text }?.second ?: backend.newRequestId()
                    requestId = text to id
                    onSend(text, id) { draft = ""; requestId = null }
                },
                enabled = draft.isNotBlank() && !sending,
            ) { Icon(Icons.AutoMirrored.Filled.Send, contentDescription = "追加指示を送る") }
        }
        Text("追加指示は新しい実行として順番に処理されます。予定や権限の変更は「設定を編集」から行ってください。",
            style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(horizontal = 12.dp, vertical = 4.dp))
    }
}

@Composable
private fun MessageBubble(m: ResidentMessageDto, backend: ResidentBackend) {
    val user = m.role == "user"
    val color = when {
        user -> MaterialTheme.colorScheme.primaryContainer
        m.kind == "notice" -> MaterialTheme.colorScheme.errorContainer
        m.kind == "question" -> MaterialTheme.colorScheme.tertiaryContainer
        else -> MaterialTheme.colorScheme.surfaceVariant
    }
    val label = when (m.kind) {
        "instruction" -> "追加指示"
        "answer" -> "回答"
        "question" -> "質問"
        "notice" -> "お知らせ"
        else -> "報告" + (m.outcome?.let { o -> " · " + runStatusStyle(RunStatus.COMPLETED, o).label }.orEmpty())
    }
    Row(Modifier.fillMaxWidth(), horizontalArrangement = if (user) Arrangement.End else Arrangement.Start) {
        Surface(color = color, shape = MaterialTheme.shapes.medium, modifier = Modifier.fillMaxWidth(if (user) 0.85f else 1f)) {
            Column(Modifier.padding(10.dp)) {
                Text("$label · ${backend.formatInstant(m.createdAt)}", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                SelectionContainer { MarkdownText(m.text) }
            }
        }
    }
}

@Composable
private fun RunHistory(
    runs: List<ResidentRunDto>,
    backend: ResidentBackend,
    modifier: Modifier,
    busy: Boolean,
    onCancel: (String) -> Unit,
    onDismiss: (String) -> Unit,
) {
    val expanded = remember { mutableStateMapOf<String, ResidentRunDto?>() }
    val scope = rememberCoroutineScope()
    if (runs.isEmpty()) {
        Box(modifier.fillMaxWidth(), contentAlignment = Alignment.Center) { Text("実行履歴はまだありません", style = MaterialTheme.typography.bodySmall) }
        return
    }
    LazyColumn(modifier, contentPadding = PaddingValues(bottom = 24.dp)) {
        items(runs, key = { it.id }) { r ->
            val style = runStatusStyle(r.status, r.outcome)
            Column(
                Modifier.fillMaxWidth().clickable {
                    if (expanded.containsKey(r.id)) expanded.remove(r.id)
                    else {
                        expanded[r.id] = null
                        scope.launch { expanded[r.id] = runCatching { backend.run(r.id) }.getOrNull() }
                    }
                }.padding(horizontal = 16.dp, vertical = 10.dp),
                verticalArrangement = Arrangement.spacedBy(2.dp),
            ) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(style.label, color = style.color, style = MaterialTheme.typography.labelLarge)
                    Text("  ${backend.formatInstant(r.startedAt ?: r.endedAt ?: r.queuedAt)} · ${r.triggers.joinToString("+") { triggerLabel(it) }}",
                        style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                (r.summary ?: r.error ?: r.reason)?.takeIf { it.isNotBlank() }?.let {
                    Text(it, style = MaterialTheme.typography.bodyMedium, maxLines = if (expanded.containsKey(r.id)) Int.MAX_VALUE else 2)
                }
                if (r.status == RunStatus.RUNNING || r.status == RunStatus.QUEUED) {
                    OutlinedButton(onClick = { onCancel(r.id) }, enabled = !busy) { Text(if (r.status == RunStatus.RUNNING) "この実行を停止" else "取り消す") }
                }
                if (r.status == RunStatus.NEEDS_RECONCILIATION) {
                    Text("Gateway の停止で中断され、外部操作の成否が分かりません。自動では再送しません。結果を確認してから閉じてください。",
                        style = MaterialTheme.typography.bodySmall, color = runStatusStyle(r.status, null).color)
                    OutlinedButton(onClick = { onDismiss(r.id) }, enabled = !busy) { Text("確認済みにする") }
                }
                if (expanded.containsKey(r.id)) RunDetail(expanded[r.id] ?: r, backend)
            }
            HorizontalDivider()
        }
    }
}

@Composable
private fun RunDetail(r: ResidentRunDto, backend: ResidentBackend) {
    val lines = buildList {
        r.scheduledFor?.let { add("予定時刻: ${backend.formatInstant(it)}") }
        r.endedAt?.let { add("終了: ${backend.formatInstant(it)}") }
        add("エージェント: ${providerLabel(r.provider.orEmpty())} ${r.model.orEmpty()} ${r.effort.orEmpty()}".trim())
        r.errorKind?.takeIf { it.isNotBlank() }?.let { add("エラー種別: $it") }
        notifyDecisionLabel(r.notifyDecision)?.let { add("通知: $it") }
        r.nextWakeAt?.let { add("再確認の予約: ${backend.formatInstant(it)}") }
        r.usage?.let { u -> add("使用量: 入力 ${u.inputTokens} / 出力 ${u.outputTokens} tokens" + (if (u.costUsd > 0) " · 約 $" + (kotlin.math.round(u.costUsd * 1000) / 1000.0) else "")) }
        r.warnings.forEach { add("注意: $it") }
        r.attempts.forEach { a -> add("試行: ${a.phase}${a.error?.takeIf { it.isNotBlank() }?.let { " – $it" }.orEmpty()}") }
    }
    Column(Modifier.padding(top = 4.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
        lines.forEach { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        r.report?.takeIf { it.isNotBlank() }?.let { SelectionContainer { MarkdownText(it) } }
        r.artifacts.forEach { a -> ArtifactRow(a.id, a.title ?: a.path, a.path, backend) }
    }
}

@Composable
private fun ArtifactRow(id: String, title: String, path: String, backend: ResidentBackend) {
    var open by remember { mutableStateOf(false) }
    var content by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(open) {
        if (open && content == null) content = try { backend.artifactText(id) } catch (e: ResidentApiException) { "開けません: ${e.message}" }
    }
    Column {
        TextButton(onClick = { open = !open }) { Text("成果物: $title") }
        if (open) {
            Text(path, style = MaterialTheme.typography.labelSmall)
            Surface(color = MaterialTheme.colorScheme.surfaceVariant, shape = MaterialTheme.shapes.small) {
                SelectionContainer { Text(content ?: "読み込み中…", fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(8.dp)) }
            }
        }
    }
}

@Composable
private fun MemoryEditor(detail: ResidentTaskDetailDto, modifier: Modifier, busy: Boolean, onSave: (String, String, Long) -> Unit) {
    val mem = detail.memory
    var editing by remember(mem.version) { mutableStateOf(false) }
    var summary by remember(mem.version) { mutableStateOf(mem.summary) }
    var stateText by remember(mem.version) { mutableStateOf(if (mem.state.isEmpty()) "" else prettyJson.encodeToString(JsonObject.serializer(), mem.state)) }
    LazyColumn(modifier, contentPadding = PaddingValues(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        item {
            Text("各実行は、この記憶（前回の状態）と最近の会話だけを引き継いで新しく実行されます。変化を確認できた実行だけが記憶を更新します。",
                style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Text("版 ${mem.version} · 更新: ${mem.updatedBy ?: "-"}", style = MaterialTheme.typography.labelSmall)
        }
        item {
            OutlinedTextField(summary, { summary = it; editing = true }, label = { Text("要約（8KiB まで）") }, modifier = Modifier.fillMaxWidth(), minLines = 3)
        }
        item {
            OutlinedTextField(stateText, { stateText = it; editing = true }, label = { Text("状態（JSON オブジェクト, 64KiB まで）") },
                textStyle = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace), modifier = Modifier.fillMaxWidth(), minLines = 6)
        }
        item {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(onClick = { onSave(summary, stateText, mem.version) }, enabled = editing && !busy) { Text("記憶を保存") }
                if (editing) TextButton(onClick = { editing = false; summary = mem.summary }) { Text("元に戻す") }
            }
            Text("実行中に編集した項目は、その実行の結果で上書きされません。", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

@Composable
private fun SettingsSummary(detail: ResidentTaskDetailDto, backend: ResidentBackend, modifier: Modifier, onEdit: () -> Unit) {
    val t = detail.task
    val rows = listOf(
        "予定" to scheduleLabel(t.schedule, backend::formatInstant) + " (${t.schedule.timezone ?: ""})",
        "エージェント" to "${providerLabel(t.provider)} · ${t.model ?: "既定モデル"} · ${t.effort ?: "既定 effort"}",
        "フォルダ" to t.cwd,
        "権限" to permissionsLabel(t.permissions) + t.permissions.allowedTools.joinToString("") { "\n  $it" },
        "通知" to buildString {
            append(when (t.notify.mode) { "always" -> "変化があれば常に"; "never" -> "通知しない"; else -> "変化があり、エージェントが必要と判断したとき" })
            if (t.notify.onQuestion) append(" · 判断待ち")
            if (t.notify.onFailure) append(" · 失敗")
            if (t.notify.cooldownMinutes > 0) append(" · クールダウン ${t.notify.cooldownMinutes}分")
            if (t.notify.quietStart != null) append(" · 静穏時間 ${t.notify.quietStart}〜${t.notify.quietEnd}")
        },
        "上限" to "1回 ${t.limits.maxRunMinutes}分 · 1日 ${t.limits.maxRunsPerDay}回" + (t.limits.expiresAt?.let { " · 期限 ${backend.formatInstant(it)}" }.orEmpty()),
    )
    LazyColumn(modifier, contentPadding = PaddingValues(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        item {
            Text("指示", style = MaterialTheme.typography.labelLarge)
            SelectionContainer { MarkdownText(t.instruction) }
        }
        items(rows) { (k, v) ->
            Text(k, style = MaterialTheme.typography.labelLarge)
            Text(v, style = MaterialTheme.typography.bodyMedium)
        }
        item { OutlinedButton(onClick = onEdit) { Text("設定を編集") } }
    }
}
