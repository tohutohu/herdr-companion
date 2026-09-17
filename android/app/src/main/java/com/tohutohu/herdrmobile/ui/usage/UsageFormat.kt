package com.tohutohu.herdrmobile.ui.usage

import com.tohutohu.herdrmobile.data.api.UsageProviderDto
import com.tohutohu.herdrmobile.data.api.UsageWindowDto
import java.time.Instant

private const val MILLIS_PER_MINUTE = 60_000L

/** The window closest to its limit; the collapsed card shows only this one. */
fun tightestWindow(provider: UsageProviderDto): UsageWindowDto? =
    provider.windows.maxByOrNull { it.usedPercent }

/** "Claude 23% · Codex 53%", the one-line form shown while the card is collapsed. */
fun usageHeadline(providers: List<UsageProviderDto>): String =
    providers.mapNotNull { p ->
        val w = tightestWindow(p) ?: return@mapNotNull null
        "${p.displayName} ${w.usedPercent}%"
    }.joinToString(" · ")

/** The collapsed form with the reset countdown for each provider's tightest window. */
fun usageHeadlineWithReset(providers: List<UsageProviderDto>, nowMillis: Long): String =
    providers.mapNotNull { p ->
        val w = tightestWindow(p) ?: return@mapNotNull null
        val reset = resetCountdown(w.resetsAt, nowMillis)
        "${p.displayName} ${w.usedPercent}%" + (reset?.let { " ($it)" } ?: "")
    }.joinToString(" · ")

/** "7d", or "7d · Fable only" for a window that covers one model. */
fun windowTitle(window: UsageWindowDto): String =
    if (window.scope.isNullOrBlank()) window.label else "${window.label} · ${window.scope}"

/** Formats the time left until a rate-limit window resets, e.g. "in 2h 15m". */
fun resetCountdown(resetsAt: String?, nowMillis: Long): String? {
    val resetMillis = resetsAt?.let { runCatching { Instant.parse(it).toEpochMilli() }.getOrNull() }
        ?: return null
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
