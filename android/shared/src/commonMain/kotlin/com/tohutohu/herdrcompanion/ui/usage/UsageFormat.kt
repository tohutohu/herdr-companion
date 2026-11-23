package com.tohutohu.herdrcompanion.ui.usage

import com.tohutohu.herdrcompanion.data.api.UsageProviderDto
import com.tohutohu.herdrcompanion.data.api.UsageWindowDto
import kotlin.math.roundToInt
import kotlin.time.Instant

private const val MILLIS_PER_MINUTE = 60_000L
private const val WEEKLY_WINDOW_MINUTES = 7 * 24 * 60

/** Formats epoch millis as "M/d HH:mm" in the device's time zone. */
internal expect fun localMonthDayTime(epochMillis: Long): String

private fun parseMillis(iso: String?): Long? =
    iso?.let { runCatching { Instant.parse(it).toEpochMilliseconds() }.getOrNull() }

/** The window closest to its limit; the collapsed card shows only this one. */
fun tightestWindow(provider: UsageProviderDto): UsageWindowDto? =
    provider.windows.maxByOrNull { it.usedPercent }

/** The collapsed form with the reset text for each provider's tightest window. */
fun usageHeadlineWithReset(
    providers: List<UsageProviderDto>,
    nowMillis: Long,
    formatDateTime: (Long) -> String = ::localMonthDayTime,
): String =
    providers.mapNotNull { p ->
        val w = tightestWindow(p) ?: return@mapNotNull null
        val reset = resetText(w, nowMillis, formatDateTime)
        "${p.displayName} ${w.usedPercent}%" + (reset?.let { " ($it)" } ?: "")
    }.joinToString(" · ")

/** Even consumption over the whole window, reaching 100% at its reset. */
fun idealUsedPercent(window: UsageWindowDto, nowMillis: Long): Int? {
    if (window.windowMinutes <= 0) return null
    val resetMillis = parseMillis(window.resetsAt) ?: return null
    // An expired cached reading cannot describe the new window's pace.
    if (resetMillis <= nowMillis) return null
    val durationMillis = window.windowMinutes.toLong() * MILLIS_PER_MINUTE
    val elapsed = 1.0 - (resetMillis.toDouble() - nowMillis.toDouble()) / durationMillis
    return (elapsed.coerceIn(0.0, 1.0) * 100).roundToInt()
}

/** "7d", or "7d · Fable only" for a window that covers one model. */
fun windowTitle(window: UsageWindowDto): String =
    if (window.scope.isNullOrBlank()) window.label else "${window.label} · ${window.scope}"

/** Uses an exact local date/time for weekly windows and a countdown otherwise. */
fun resetText(
    window: UsageWindowDto,
    nowMillis: Long,
    formatDateTime: (Long) -> String = ::localMonthDayTime,
): String? {
    val weekly = window.windowMinutes >= WEEKLY_WINDOW_MINUTES || window.label == "7d"
    return if (weekly) parseMillis(window.resetsAt)?.let(formatDateTime) else resetCountdown(window.resetsAt, nowMillis)
}

/** Formats the time left until a rate-limit window resets, e.g. "in 2h 15m". */
fun resetCountdown(resetsAt: String?, nowMillis: Long): String? {
    val resetMillis = parseMillis(resetsAt) ?: return null
    val millisLeft = resetMillis - nowMillis
    if (millisLeft <= 0) return "now"

    val minutesLeft = (millisLeft + MILLIS_PER_MINUTE - 1) / MILLIS_PER_MINUTE
    val days = minutesLeft / (24 * 60)
    val hours = (minutesLeft % (24 * 60)) / 60
    val minutes = minutesLeft % 60
    val duration = when {
        days > 0 && hours > 0 -> "${days}d ${hours}h"
        days > 0 -> "${days}d"
        hours > 0 && minutes > 0 -> "${hours}h ${minutes}m"
        hours > 0 -> "${hours}h"
        else -> "${minutes}m"
    }
    return "in $duration"
}
