package com.tohutohu.herdrmobile.ui.usage

import com.tohutohu.herdrmobile.data.api.UsageProviderDto
import com.tohutohu.herdrmobile.data.api.UsageWindowDto

/** The window closest to its limit; the collapsed card shows only this one. */
fun tightestWindow(provider: UsageProviderDto): UsageWindowDto? =
    provider.windows.maxByOrNull { it.usedPercent }

/** "Claude 23% · Codex 53%", the one-line form shown while the card is collapsed. */
fun usageHeadline(providers: List<UsageProviderDto>): String =
    providers.mapNotNull { p ->
        val w = tightestWindow(p) ?: return@mapNotNull null
        "${p.displayName} ${w.usedPercent}%"
    }.joinToString(" · ")

/** "7d", or "7d · Fable only" for a window that covers one model. */
fun windowTitle(window: UsageWindowDto): String =
    if (window.scope.isNullOrBlank()) window.label else "${window.label} · ${window.scope}"
