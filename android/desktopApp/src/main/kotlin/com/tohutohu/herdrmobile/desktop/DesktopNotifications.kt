package com.tohutohu.herdrmobile.desktop

import com.tohutohu.herdrmobile.data.api.Status
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

/** macOS notification bridge. It deliberately does not create another tray/menu-bar owner. */
object DesktopNotificationService {
    suspend fun show(event: DesktopNotificationEvent) = withContext(Dispatchers.IO) {
        if (!System.getProperty("os.name").contains("mac", ignoreCase = true)) return@withContext
        val title = appleScriptString(event.title)
        val body = appleScriptString(event.body)
        runCatching {
            ProcessBuilder(
                "/usr/bin/osascript",
                "-e",
                "display notification \"$body\" with title \"$title\"",
            ).start().waitFor()
        }
    }

    private fun appleScriptString(value: String): String = value
        .replace("\\", "\\\\")
        .replace("\"", "\\\"")
        .replace('\n', ' ')
        .take(240)
}
