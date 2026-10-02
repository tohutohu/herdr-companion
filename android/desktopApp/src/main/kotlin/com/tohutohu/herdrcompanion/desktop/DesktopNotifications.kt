package com.tohutohu.herdrcompanion.desktop

import com.tohutohu.herdrcompanion.data.api.Status
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

data class DesktopNotificationEvent(
    val id: String,
    val sessionId: String? = null,
    val title: String,
    val body: String,
)

/** Only meaningful state transitions create notifications; polling is otherwise silent. */
fun notificationForStatusTransition(
    sessionId: String,
    project: String,
    previous: String?,
    next: String,
): DesktopNotificationEvent? {
    if (previous == null || previous == next) return null
    val body = when (next) {
        Status.COMPLETED -> "Agent finished"
        Status.WAITING_INPUT -> "Your answer is needed"
        Status.WAITING_APPROVAL -> "Approval is needed"
        Status.FAILED -> "Agent failed"
        else -> return null
    }
    return DesktopNotificationEvent(
        id = "$sessionId:$next:${System.nanoTime()}",
        sessionId = sessionId,
        title = project.ifBlank { "Herdr session" },
        body = body,
    )
}

/**
 * Notification bridge for macOS (`osascript`) and Linux (`notify-send`). It
 * deliberately does not create another tray/menu-bar owner, so Windows has none.
 */
object DesktopNotificationService {
    suspend fun show(event: DesktopNotificationEvent) = withContext(Dispatchers.IO) {
        val command = notificationCommand(event, DesktopOs.current) ?: return@withContext
        runCatching { ProcessBuilder(command).start().waitFor() }
    }
}

internal fun notificationCommand(event: DesktopNotificationEvent, os: DesktopOs): List<String>? = when (os) {
    DesktopOs.MAC -> {
        val title = appleScriptString(event.title)
        val body = appleScriptString(event.body)
        listOf("/usr/bin/osascript", "-e", "display notification \"$body\" with title \"$title\"")
    }
    // `--` keeps a title that starts with `-` from being read as an option.
    DesktopOs.LINUX -> listOf("notify-send", "--app-name=Herdr Companion", "--", event.title.take(240), event.body.take(240))
    DesktopOs.WINDOWS -> null
}

private fun appleScriptString(value: String): String = value
    .replace("\\", "\\\\")
    .replace("\"", "\\\"")
    .replace('\n', ' ')
    .take(240)
