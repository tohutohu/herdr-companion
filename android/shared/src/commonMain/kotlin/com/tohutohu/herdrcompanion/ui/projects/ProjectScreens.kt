package com.tohutohu.herdrcompanion.ui.projects

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.NotificationImportant
import androidx.compose.material.icons.filled.Notifications
import androidx.compose.material.icons.filled.NotificationsOff
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Badge
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.PrimaryTabRow
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Surface
import androidx.compose.material3.Tab
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.data.api.NotifyLevel
import com.tohutohu.herdrcompanion.ui.resident.ResidentBackend
import com.tohutohu.herdrcompanion.ui.resident.ResidentTaskRow
import com.tohutohu.herdrcompanion.ui.sessions.SessionRow

/** The icon of a notification level. */
fun notifyLevelIcon(level: String): ImageVector = when (level) {
    NotifyLevel.ATTENTION -> Icons.Default.NotificationImportant
    NotifyLevel.OFF -> Icons.Default.NotificationsOff
    else -> Icons.Default.Notifications
}

/** A small mark for something whose project's notifications are turned down; nothing for "all". */
@Composable
fun ProjectNotifyMark(level: String?, modifier: Modifier = Modifier) {
    if (level == null || level == NotifyLevel.ALL) return
    Icon(
        notifyLevelIcon(level),
        contentDescription = "通知: ${notifyLevelLabel(level)}",
        tint = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = modifier.size(14.dp),
    )
}

/** State of the Projects tab, kept by the caller so switching tabs keeps it. */
data class ProjectsUiState(
    /** Null until the projects were loaded once. */
    val groups: List<ProjectGroup>? = null,
    val error: String? = null,
    /** The gateway has no projects (older than the app). */
    val unsupported: Boolean = false,
)

/**
 * The Projects tab: every project (favorite folder) with how much is
 * running, waiting for the user and unread, and its notification level.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ProjectListScreen(
    state: ProjectsUiState,
    onOpen: (key: String) -> Unit,
    onRefresh: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Scaffold(
        modifier = modifier,
        topBar = {
            TopAppBar(
                title = { Text("Projects") },
                actions = {
                    IconButton(onClick = onRefresh) { Icon(Icons.Default.Refresh, contentDescription = "再読み込み") }
                },
            )
        },
    ) { padding ->
        val groups = state.groups
        when {
            state.unsupported -> Message("この Gateway はプロジェクトに対応していません。Mac の Gateway を更新してください。", Modifier.padding(padding))
            groups == null -> Message(state.error ?: "読み込み中…", Modifier.padding(padding))
            groups.isEmpty() -> Message(
                "お気に入りのフォルダがプロジェクトになります。新しいセッションの画面でフォルダに★を付けると、ここに並びます。",
                Modifier.padding(padding),
            )
            else -> LazyColumn(
                Modifier.fillMaxSize().padding(padding),
                contentPadding = PaddingValues(horizontal = 12.dp, vertical = 8.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                state.error?.let { item { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) } }
                items(groups, key = { it.key }) { group -> ProjectCard(group, onClick = { onOpen(group.key) }) }
            }
        }
    }
}

@Composable
private fun Message(text: String, modifier: Modifier = Modifier) {
    Box(modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) {
        Text(text, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable
private fun ProjectCard(group: ProjectGroup, onClick: () -> Unit) {
    Card(
        onClick = onClick,
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainerHigh),
        modifier = Modifier.fillMaxWidth().semantics { contentDescription = "プロジェクト ${group.name}" },
    ) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    group.name,
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = if (group.unread > 0 || group.needsYou > 0) FontWeight.Bold else FontWeight.SemiBold,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
                if (group.unread > 0) {
                    Badge { Text("${group.unread}") }
                    Spacer(Modifier.width(8.dp))
                }
                if (group.project != null) {
                    Icon(
                        notifyLevelIcon(group.notify),
                        contentDescription = "通知: ${notifyLevelLabel(group.notify)}",
                        tint = if (group.notify == NotifyLevel.ALL) MaterialTheme.colorScheme.onSurfaceVariant else MaterialTheme.colorScheme.primary,
                        modifier = Modifier.size(20.dp),
                    )
                }
            }
            Text(
                group.project?.path ?: "どのプロジェクトにも入らないセッションと常駐",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.CenterVertically) {
                if (group.running > 0) Stat("実行中 ${group.running}", MaterialTheme.colorScheme.primary)
                if (group.needsYou > 0) Stat("要対応 ${group.needsYou}", MaterialTheme.colorScheme.error)
                Stat("セッション ${group.sessions.size}", MaterialTheme.colorScheme.onSurfaceVariant)
                group.tasks?.let { Stat("常駐 ${it.size}", MaterialTheme.colorScheme.onSurfaceVariant) }
            }
        }
    }
}

@Composable
private fun Stat(text: String, color: Color) {
    Text(text, style = MaterialTheme.typography.labelMedium, color = color)
}

/**
 * One project: its notification level, and its sessions and resident tasks
 * on two tabs, with a button to start either here. "その他" has no settings
 * and no button. [residentBackend] is null when the gateway has no resident
 * tasks.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ProjectDetailScreen(
    group: ProjectGroup?,
    residentBackend: ResidentBackend?,
    notifyBusy: Boolean,
    error: String?,
    onBack: () -> Unit,
    onNotify: (String) -> Unit,
    onOpenSession: (String) -> Unit,
    onOpenTask: (String) -> Unit,
    onNewSession: (cwd: String) -> Unit,
    onNewTask: (cwd: String) -> Unit,
    modifier: Modifier = Modifier,
) {
    var tab by rememberSaveable { mutableIntStateOf(0) }
    val project = group?.project
    Scaffold(
        modifier = modifier,
        topBar = {
            TopAppBar(
                title = {
                    Column {
                        Text(group?.name ?: "プロジェクト", maxLines = 1, overflow = TextOverflow.Ellipsis)
                        project?.let {
                            Text(it.path, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        }
                    }
                },
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "戻る") }
                },
            )
        },
        floatingActionButton = {
            if (project != null) {
                val forTasks = tab == 1
                if (!forTasks || residentBackend != null) {
                    ExtendedFloatingActionButton(
                        onClick = { if (forTasks) onNewTask(project.path) else onNewSession(project.path) },
                        icon = { Icon(Icons.Default.Add, contentDescription = null) },
                        text = { Text(if (forTasks) "ここで常駐を作る" else "ここで新規セッション") },
                    )
                }
            }
        },
    ) { padding ->
        if (group == null) {
            Message(error ?: "このプロジェクトは見つかりません。お気に入りから外された可能性があります。", Modifier.padding(padding))
            return@Scaffold
        }
        Column(Modifier.fillMaxSize().padding(padding)) {
            if (project != null) NotifySetting(project.notify, notifyBusy, onNotify)
            error?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(horizontal = 16.dp)) }
            PrimaryTabRow(selectedTabIndex = tab) {
                Tab(selected = tab == 0, onClick = { tab = 0 }, text = { Text("セッション ${group.sessions.size}") })
                Tab(selected = tab == 1, onClick = { tab = 1 }, text = { Text("常駐 ${group.tasks?.size ?: "–"}") })
            }
            LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 88.dp)) {
                if (tab == 0) {
                    if (group.sessions.isEmpty()) item { EmptyLine("このプロジェクトのセッションはありません") }
                    items(group.sessions, key = { it.session.id }) { item ->
                        SessionRow(
                            s = item.session,
                            relativeUpdatedAt = item.relativeUpdatedAt,
                            selected = false,
                            selecting = false,
                            onClick = { onOpenSession(item.session.id) },
                            onLongClick = { onOpenSession(item.session.id) },
                            color = item.color,
                        )
                        HorizontalDivider()
                    }
                } else {
                    val tasks = group.tasks
                    when {
                        residentBackend == null || tasks == null -> item { EmptyLine("常駐タスクを読み込めていません") }
                        tasks.isEmpty() -> item { EmptyLine("このプロジェクトの常駐タスクはありません") }
                        else -> items(tasks, key = { it.id }) { task ->
                            ResidentTaskRow(task, residentBackend, selected = false, onClick = { onOpenTask(task.id) })
                            HorizontalDivider()
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun EmptyLine(text: String) {
    Text(text, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(24.dp))
}

private val LEVELS = listOf(NotifyLevel.ALL, NotifyLevel.ATTENTION, NotifyLevel.OFF)

@Composable
private fun NotifySetting(level: String, busy: Boolean, onNotify: (String) -> Unit) {
    Surface(
        color = MaterialTheme.colorScheme.surfaceContainer,
        shape = MaterialTheme.shapes.medium,
        modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 8.dp),
    ) {
        Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text("通知（このプロジェクトのセッションと常駐）", style = MaterialTheme.typography.labelLarge)
            SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
                LEVELS.forEachIndexed { i, l ->
                    SegmentedButton(
                        selected = level == l,
                        onClick = { if (level != l) onNotify(l) },
                        enabled = !busy,
                        shape = SegmentedButtonDefaults.itemShape(i, LEVELS.size),
                        icon = { Icon(notifyLevelIcon(l), contentDescription = null, modifier = Modifier.size(SegmentedButtonDefaults.IconSize)) },
                        label = { Text(notifyLevelLabel(l), maxLines = 1) },
                    )
                }
            }
            Text(
                when (level) {
                    NotifyLevel.ATTENTION -> "質問・承認・失敗だけ通知します。完了や常駐の報告は通知しません。"
                    NotifyLevel.OFF -> "通知しません。一覧と未読の印はそのまま残ります。"
                    else -> "完了・質問・承認・失敗と常駐の報告をすべて通知します。"
                },
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}
