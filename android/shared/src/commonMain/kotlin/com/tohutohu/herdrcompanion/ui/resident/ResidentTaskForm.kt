package com.tohutohu.herdrcompanion.ui.resident

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Checkbox
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.data.api.DirListingDto
import com.tohutohu.herdrcompanion.data.api.ModelsResponse
import com.tohutohu.herdrcompanion.data.api.PrecheckDto
import com.tohutohu.herdrcompanion.data.api.ResidentTaskDto
import com.tohutohu.herdrcompanion.data.api.ResidentTaskRequest
import com.tohutohu.herdrcompanion.data.api.ScheduleDto
import com.tohutohu.herdrcompanion.data.api.ScheduleKind
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * The form's editable values: as few as starting a session. Permissions,
 * notifications and limits keep the gateway's defaults (or the task's own
 * values when editing).
 */
data class ResidentDraft(
    /** Optional: the gateway names a task after its instruction's first line. */
    val name: String = "",
    val instruction: String = "",
    val provider: String = "claude",
    val model: String = "",
    val effort: String = "",
    val cwd: String = "",
    val kind: String = ScheduleKind.DAILY,
    val time: String = "07:00",
    val days: Set<String> = setOf("mon", "tue", "wed", "thu", "fri"),
    val cron: String = "0 7 * * 1-5",
    /** RFC 3339 for a one-off run. */
    val at: String = "",
    val timezone: String = "Asia/Tokyo",
    /** Shell command run before automatic runs; blank for none. */
    val precheck: String = "",
    val precheckTimeoutSeconds: Int? = null,
    /** The task being edited has a precheck, so a blank command removes it. */
    val hadPrecheck: Boolean = false,
) {
    fun schedule() = ScheduleDto(
        kind = kind,
        time = time.takeIf { kind == ScheduleKind.DAILY || kind == ScheduleKind.WEEKLY },
        days = weekdayKeys.filter { it in days }.takeIf { kind == ScheduleKind.WEEKLY },
        cron = cron.takeIf { kind == ScheduleKind.CRON },
        at = at.takeIf { kind == ScheduleKind.ONCE },
        timezone = timezone,
    )

    /** The fields this form owns; anything else stays as it is. */
    fun request(trust: Boolean, requestId: String? = null, expectedVersion: Long? = null) = ResidentTaskRequest(
        name = name.trim().ifEmpty { null }, instruction = instruction, provider = provider,
        model = if (expectedVersion == null) model.ifBlank { null } else model,
        effort = if (expectedVersion == null) effort.ifBlank { null } else effort,
        cwd = cwd, schedule = schedule(), trust = trust.takeIf { it },
        precheck = when {
            precheck.isNotBlank() -> PrecheckDto(precheck.trim(), precheckTimeoutSeconds)
            expectedVersion != null && hadPrecheck -> PrecheckDto("")
            else -> null
        },
        requestId = requestId, expectedVersion = expectedVersion,
    )

    val complete get() = instruction.isNotBlank() && cwd.isNotBlank()

    companion object {
        fun from(t: ResidentTaskDto) = ResidentDraft(
            name = t.name, instruction = t.instruction, provider = t.provider, model = t.model.orEmpty(), effort = t.effort.orEmpty(),
            cwd = t.cwd, kind = t.schedule.kind, time = t.schedule.time ?: "07:00", days = t.schedule.days?.toSet() ?: emptySet(),
            cron = t.schedule.cron ?: "0 7 * * 1-5", at = t.schedule.at.orEmpty(), timezone = t.schedule.timezone ?: "Asia/Tokyo",
            precheck = t.precheck?.command.orEmpty(), precheckTimeoutSeconds = t.precheck?.timeoutSeconds, hadPrecheck = t.precheck != null,
        )
    }
}

val commonTimezones = listOf("Asia/Tokyo", "UTC", "America/Los_Angeles", "America/New_York", "Europe/London", "Europe/Berlin", "Asia/Singapore")

/**
 * Creates a task, or edits [existingId]. Before saving it shows the next
 * three run times the gateway computes for the schedule; the gateway refuses
 * an invalid cron, timezone, model, effort or folder with the reason, shown
 * here. A folder the agent never trusted needs an explicit confirmation.
 */
@OptIn(ExperimentalMaterial3Api::class, ExperimentalLayoutApi::class)
@Composable
fun ResidentTaskForm(
    backend: ResidentBackend,
    existingId: String?,
    defaultTimezone: String,
    onSaved: (String) -> Unit,
    onCancel: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var existing by remember { mutableStateOf<ResidentTaskDto?>(null) }
    var draft by remember { mutableStateOf(ResidentDraft(timezone = defaultTimezone)) }
    var loadError by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(existingId) {
        if (existingId != null) {
            try {
                val t = backend.task(existingId).task
                existing = t
                draft = ResidentDraft.from(t)
            } catch (e: ResidentApiException) {
                loadError = e.message
            }
        }
    }
    var models by remember { mutableStateOf<ModelsResponse?>(null) }
    LaunchedEffect(draft.provider) {
        models = runCatching { backend.models(draft.provider) }.getOrNull()
    }
    var preview by remember { mutableStateOf<List<String>>(emptyList()) }
    var previewError by remember { mutableStateOf<String?>(null) }
    val schedule = draft.schedule()
    LaunchedEffect(schedule) {
        delay(400)
        if (schedule.kind == ScheduleKind.MANUAL) {
            preview = emptyList(); previewError = null
            return@LaunchedEffect
        }
        try {
            preview = backend.preview(schedule).next
            previewError = null
        } catch (e: CancellationException) {
            throw e
        } catch (e: ResidentApiException) {
            preview = emptyList()
            previewError = e.message
        }
    }
    var saving by remember { mutableStateOf(false) }
    var saveError by remember { mutableStateOf<String?>(null) }
    var trustNeeded by remember { mutableStateOf(false) }
    var trust by remember { mutableStateOf(false) }
    var browse by remember { mutableStateOf(false) }
    // A create keeps its request id across retries, so a lost answer does not make two tasks.
    val createRequestId = rememberSaveable { backend.newRequestId() }
    val scope = rememberCoroutineScope()

    fun save() {
        saving = true
        saveError = null
        scope.launch {
            try {
                val d = draft
                val id = if (existingId == null) {
                    backend.create(d.request(trust, requestId = createRequestId)).id
                } else {
                    backend.update(existingId, d.request(trust, expectedVersion = existing?.version)).id
                }
                onSaved(id)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ResidentApiException) {
                trustNeeded = trustNeeded || e.trustRequired
                saveError = when {
                    e.conflict && existingId != null -> "ほかの場所で変更されました。開き直してから編集してください: ${e.message}"
                    e.unreachable -> "Gateway に接続できません。保存されていません: ${e.message}"
                    else -> e.message
                }
            } finally {
                saving = false
            }
        }
    }

    Scaffold(
        modifier = modifier,
        topBar = {
            TopAppBar(
                title = { Text(if (existingId == null) "新しい常駐タスク" else "設定を編集") },
                navigationIcon = { IconButton(onClick = onCancel) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "戻る") } },
                actions = {
                    TextButton(onClick = { save() }, enabled = !saving && draft.complete) {
                        Text(if (saving) "保存中…" else "保存")
                    }
                },
            )
        },
    ) { padding ->
        Column(
            Modifier.fillMaxSize().padding(padding).imePadding().verticalScroll(rememberScrollState()).padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            loadError?.let { ErrorBanner("読み込めません: $it") }
            saveError?.let { ErrorBanner(it) }
            if (trustNeeded) {
                Row(Modifier.fillMaxWidth().clickable { trust = !trust }, verticalAlignment = Alignment.CenterVertically) {
                    Checkbox(trust, { trust = it })
                    Text("このフォルダを信頼する: プロジェクトの hooks や MCP サーバーが無人で実行されます。内容を確認してから選んでください。",
                        style = MaterialTheme.typography.bodySmall)
                }
            }
            OutlinedTextField(
                draft.instruction, { draft = draft.copy(instruction = it) },
                label = { Text("指示") },
                placeholder = { Text("例: 毎朝このリポジトリの CI と PR を確認し、対応が必要なものがあれば知らせて") },
                modifier = Modifier.fillMaxWidth().heightIn(min = 120.dp), minLines = 4,
            )
            Section("フォルダ")
            Row(verticalAlignment = Alignment.CenterVertically) {
                OutlinedTextField(draft.cwd, { draft = draft.copy(cwd = it) }, label = { Text("作業フォルダ（workspaceRoots 内）") }, modifier = Modifier.weight(1f), singleLine = true)
                IconButton(onClick = { browse = true }) { Icon(Icons.Default.Folder, contentDescription = "フォルダを選ぶ") }
            }
            Section("エージェント")
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                listOf("claude", "codex").forEach { p ->
                    FilterChip(draft.provider == p, { draft = draft.copy(provider = p, model = "", effort = "") }, label = { Text(providerLabel(p)) })
                }
            }
            ModelPickers(draft, models) { draft = it }
            Section("予定")
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                listOf(ScheduleKind.ONCE to "1回", ScheduleKind.DAILY to "毎日", ScheduleKind.WEEKLY to "毎週", ScheduleKind.CRON to "cron", ScheduleKind.MANUAL to "手動のみ").forEach { (k, l) ->
                    FilterChip(draft.kind == k, { draft = draft.copy(kind = k) }, label = { Text(l) })
                }
            }
            when (draft.kind) {
                ScheduleKind.DAILY, ScheduleKind.WEEKLY -> {
                    OutlinedTextField(draft.time, { draft = draft.copy(time = it) }, label = { Text("時刻 (HH:MM)") }, singleLine = true)
                    if (draft.kind == ScheduleKind.WEEKLY) {
                        FlowRow(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                            weekdayKeys.forEach { d ->
                                FilterChip(d in draft.days, { draft = draft.copy(days = if (d in draft.days) draft.days - d else draft.days + d) }, label = { Text(weekdayLabel(d)) })
                            }
                        }
                    }
                }
                ScheduleKind.CRON -> OutlinedTextField(draft.cron, { draft = draft.copy(cron = it) }, label = { Text("cron（分 時 日 月 曜日）") }, singleLine = true)
                ScheduleKind.ONCE -> OutlinedTextField(draft.at, { draft = draft.copy(at = it) }, label = { Text("日時 (例: 2026-11-01T07:00:00+09:00)") }, singleLine = true)
            }
            if (draft.kind != ScheduleKind.MANUAL && draft.kind != ScheduleKind.ONCE) TimezonePicker(draft.timezone) { draft = draft.copy(timezone = it) }
            when {
                draft.kind == ScheduleKind.MANUAL -> Text("自動では実行しません（今すぐ実行・追加指示・エージェントの再確認予約のみ）", style = MaterialTheme.typography.bodySmall)
                previewError != null -> Text("予定が正しくありません: $previewError", color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
                preview.isNotEmpty() -> Text("次の実行: " + preview.joinToString(" / ") { backend.formatInstant(it) }, style = MaterialTheme.typography.bodySmall)
                else -> Text("予定された実行はありません", style = MaterialTheme.typography.bodySmall)
            }
            Section("事前チェック（省略可）")
            OutlinedTextField(
                draft.precheck, { draft = draft.copy(precheck = it) },
                label = { Text("コマンド") },
                placeholder = { Text("例: python3 scrape.py") },
                textStyle = MaterialTheme.typography.bodyMedium.copy(fontFamily = FontFamily.Monospace),
                modifier = Modifier.fillMaxWidth(), minLines = 1, maxLines = 6,
            )
            Text(
                "自動実行の前にフォルダで実行します。終了コード 0 ならエージェントを起動して出力を渡し、1 なら起動せずに終えます" +
                    "（1日の上限に数えません）。それ以外は失敗として知らせます。標準入力には記憶（JSON）が入ります。" +
                    "コマンドは権限設定の対象外で、あなたの権限で動きます。",
                style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            OutlinedTextField(
                draft.name, { draft = draft.copy(name = it) },
                label = { Text("タスク名（省略可）") },
                placeholder = { Text("指示の1行目") },
                modifier = Modifier.fillMaxWidth(), singleLine = true,
            )
            Text("フォルダ内のファイル編集とネットワークは使えます。人の承認が必要な操作は無人では行わず、実行は「進められず」として知らせます。",
                style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            HorizontalDivider()
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(onClick = { save() }, enabled = !saving && draft.complete) {
                    Text(if (saving) "保存中…" else "保存")
                }
                OutlinedButton(onClick = onCancel) { Text("キャンセル") }
            }
        }
    }
    if (browse) {
        FolderBrowser(backend, start = draft.cwd.ifBlank { null }, onPick = { draft = draft.copy(cwd = it); browse = false }, onDismiss = { browse = false })
    }
}

@Composable
private fun Section(title: String) {
    Text(title, style = MaterialTheme.typography.titleSmall, modifier = Modifier.padding(top = 8.dp))
}

@Composable
private fun ModelPickers(draft: ResidentDraft, models: ModelsResponse?, onChange: (ResidentDraft) -> Unit) {
    val modelOptions = listOf("" to "既定") + models?.models.orEmpty().map { it.id to it.name }
    val efforts = (models?.models?.firstOrNull { it.id == draft.model }?.efforts?.takeIf { it.isNotEmpty() } ?: models?.efforts.orEmpty())
    val effortOptions = listOf("" to "既定") + efforts.map { it.id to it.name }
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Dropdown("モデル", draft.model, modelOptions, Modifier.weight(1f)) { onChange(draft.copy(model = it, effort = "")) }
        Dropdown("effort", draft.effort, effortOptions, Modifier.weight(1f)) { onChange(draft.copy(effort = it)) }
    }
    if (models == null) Text("モデル一覧を取得できません（既定を使うか、保存時に Gateway が確認します）", style = MaterialTheme.typography.bodySmall)
}

@Composable
private fun Dropdown(label: String, value: String, options: List<Pair<String, String>>, modifier: Modifier, onPick: (String) -> Unit) {
    var open by remember { mutableStateOf(false) }
    Column(modifier) {
        OutlinedButton(onClick = { open = true }, modifier = Modifier.fillMaxWidth()) {
            Text("$label: ${options.firstOrNull { it.first == value }?.second ?: value.ifBlank { "既定" }}")
        }
        DropdownMenu(open, onDismissRequest = { open = false }) {
            options.forEach { (id, name) -> DropdownMenuItem(text = { Text(name) }, onClick = { onPick(id); open = false }) }
        }
    }
}

@Composable
private fun TimezonePicker(value: String, onChange: (String) -> Unit) {
    var open by remember { mutableStateOf(false) }
    Row(verticalAlignment = Alignment.CenterVertically) {
        OutlinedTextField(value, onChange, label = { Text("タイムゾーン (IANA)") }, singleLine = true, modifier = Modifier.weight(1f))
        TextButton(onClick = { open = true }) { Text("選ぶ") }
        DropdownMenu(open, onDismissRequest = { open = false }) {
            commonTimezones.forEach { tz -> DropdownMenuItem(text = { Text(tz) }, onClick = { onChange(tz); open = false }) }
        }
    }
}

@Composable
private fun FolderBrowser(backend: ResidentBackend, start: String?, onPick: (String) -> Unit, onDismiss: () -> Unit) {
    var path by remember { mutableStateOf<String?>(null) }
    var listing by remember { mutableStateOf<DirListingDto?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(path) {
        listing = null
        listing = try { backend.directories(path) } catch (e: ResidentApiException) { error = e.message; null }
    }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(path ?: "ワークスペース") },
        text = {
            Column {
                error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
                val entries = listing?.entries.orEmpty()
                LazyColumn(Modifier.heightIn(max = 360.dp)) {
                    listing?.parent?.let { p -> item { Text("..", Modifier.fillMaxWidth().clickable { path = p }.padding(10.dp)) } }
                    items(entries) { e -> Text(e.name, Modifier.fillMaxWidth().clickable { path = e.path }.padding(10.dp)) }
                }
            }
        },
        confirmButton = { TextButton(onClick = { path?.let(onPick) }, enabled = path != null) { Text("このフォルダ") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("キャンセル") } },
    )
}
