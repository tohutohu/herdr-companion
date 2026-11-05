package com.tohutohu.herdrcompanion.ui.resident

import androidx.compose.ui.graphics.Color
import com.tohutohu.herdrcompanion.data.api.PermissionProfile
import com.tohutohu.herdrcompanion.data.api.PermissionsDto
import com.tohutohu.herdrcompanion.data.api.RunStatus
import com.tohutohu.herdrcompanion.data.api.ScheduleDto
import com.tohutohu.herdrcompanion.data.api.ScheduleKind
import com.tohutohu.herdrcompanion.data.api.TaskState

/** How a task or run state is shown. */
data class ResidentStyle(val label: String, val color: Color)

private val green = Color(0xFF2E7D32)
private val orange = Color(0xFFEF6C00)
private val red = Color(0xFFC62828)
private val blue = Color(0xFF1565C0)
private val grey = Color(0xFF757575)

fun taskStateStyle(state: String): ResidentStyle = when (state) {
    TaskState.RUNNING -> ResidentStyle("実行中", green)
    TaskState.QUEUED -> ResidentStyle("待機中", blue)
    TaskState.WAITING_USER -> ResidentStyle("判断待ち", orange)
    TaskState.NEEDS_ATTENTION -> ResidentStyle("要確認", red)
    TaskState.PAUSED -> ResidentStyle("一時停止", grey)
    TaskState.IDLE -> ResidentStyle("稼働中", grey)
    else -> ResidentStyle(state, grey)
}

fun runStatusStyle(status: String, outcome: String?): ResidentStyle = when (status) {
    RunStatus.QUEUED -> ResidentStyle("待機中", blue)
    RunStatus.RUNNING -> ResidentStyle("実行中", green)
    RunStatus.COMPLETED -> when (outcome) {
        "changed" -> ResidentStyle("変化あり", blue)
        "unchanged" -> ResidentStyle("変化なし", grey)
        "blocked" -> ResidentStyle("進められず", orange)
        else -> ResidentStyle("完了", grey)
    }
    RunStatus.WAITING_USER -> ResidentStyle("判断待ち", orange)
    RunStatus.FAILED -> ResidentStyle("失敗", red)
    RunStatus.TIMED_OUT -> ResidentStyle("時間切れ", red)
    RunStatus.CANCELLED -> ResidentStyle("停止", grey)
    RunStatus.NEEDS_RECONCILIATION -> ResidentStyle("成否不明", red)
    RunStatus.SKIPPED -> ResidentStyle("スキップ", grey)
    RunStatus.DISMISSED -> ResidentStyle("確認済み", grey)
    else -> ResidentStyle(status, grey)
}

fun triggerLabel(trigger: String): String = when (trigger) {
    "schedule" -> "予定"
    "catch_up" -> "補完"
    "self_wake" -> "再確認"
    "manual" -> "手動"
    "follow_up" -> "追加指示"
    "answer" -> "回答"
    else -> trigger
}

private val dayLabels = mapOf("mon" to "月", "tue" to "火", "wed" to "水", "thu" to "木", "fri" to "金", "sat" to "土", "sun" to "日")
val weekdayKeys = listOf("mon", "tue", "wed", "thu", "fri", "sat", "sun")
fun weekdayLabel(key: String) = dayLabels[key] ?: key

/** "毎日 07:00", "毎週 月・木 07:00", "cron 0 7 * * 1-5" … with the timezone when it is not the default. */
fun scheduleLabel(s: ScheduleDto, formatInstant: (String) -> String, defaultTimezone: String = "Asia/Tokyo"): String {
    val tz = s.timezone?.takeIf { it != defaultTimezone }?.let { " ($it)" }.orEmpty()
    return when (s.kind) {
        ScheduleKind.DAILY -> "毎日 ${s.time}$tz"
        ScheduleKind.WEEKLY -> "毎週 ${s.days.orEmpty().joinToString("・") { weekdayLabel(it) }} ${s.time}$tz"
        ScheduleKind.CRON -> "cron ${s.cron}$tz"
        ScheduleKind.ONCE -> "1回 ${s.at?.let(formatInstant).orEmpty()}"
        else -> "手動のみ"
    }
}

fun permissionsLabel(p: PermissionsDto): String {
    val parts = mutableListOf(if (p.profile == PermissionProfile.WORKSPACE_WRITE) "フォルダ内の編集可" else "読み取りのみ")
    parts += "ネットワーク可"
    if (p.allowedTools.isNotEmpty()) parts += "追加許可 ${p.allowedTools.size}件"
    return parts.joinToString(" · ")
}

fun notifyDecisionLabel(d: String?): String? = when (d) {
    null, "" -> null
    "queued" -> "通知した"
    "deferred_quiet_hours" -> "静穏時間のため保留"
    "not_requested" -> "通知不要と判断"
    "disabled" -> "通知オフ"
    "duplicate_key" -> "通知済みの内容"
    "cooldown" -> "クールダウン中"
    "failure_already_reported" -> "失敗は通知済み"
    else -> d
}

fun providerLabel(p: String) = when (p) {
    "claude" -> "Claude Code"
    "codex" -> "Codex"
    "dsh" -> "DeepSeek Harness"
    else -> p
}
