package com.tohutohu.herdrcompanion.ui

import kotlin.time.Duration.Companion.days
import kotlin.time.Duration.Companion.hours
import kotlin.time.Duration.Companion.minutes
import kotlin.time.Instant

/** Marks work a session left running. Not a status: a done session can have some. */
const val BACKGROUND_SYMBOL = "◐"

fun backgroundLabel(count: Int): String = "$count in background"

fun backgroundKindLabel(kind: String): String = when (kind) {
    "agent" -> "Agent"
    "shell" -> "Shell"
    else -> "Task"
}

/** How long a task has been running, or null when its start time does not parse. */
fun backgroundElapsed(startedAt: String, now: Instant): String? {
    val started = runCatching { Instant.parse(startedAt) }.getOrNull() ?: return null
    val elapsed = now - started
    return when {
        elapsed < 1.minutes -> "now"
        elapsed < 1.hours -> "${elapsed.inWholeMinutes}m"
        elapsed < 1.days -> "${elapsed.inWholeHours}h"
        else -> "${elapsed.inWholeDays}d"
    }
}
