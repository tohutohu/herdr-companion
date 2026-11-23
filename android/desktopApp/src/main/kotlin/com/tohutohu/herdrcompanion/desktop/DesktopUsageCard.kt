package com.tohutohu.herdrcompanion.desktop

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import com.tohutohu.herdrcompanion.data.api.GatewayApi
import com.tohutohu.herdrcompanion.data.api.UsageDto
import com.tohutohu.herdrcompanion.ui.usage.UsageCard
import com.tohutohu.herdrcompanion.ui.usage.UsageCardAction
import com.tohutohu.herdrcompanion.ui.usage.UsageCardUiState
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import java.time.Duration
import java.time.Instant

/** The gateway serves a cached reading, so polling it is cheap. */
private const val USAGE_POLL_MS = 60_000L

/** The plan limits above the session list, polled like the Android card. */
@Composable
internal fun DesktopUsageCard(api: GatewayApi) {
    var usage by remember { mutableStateOf<UsageDto?>(null) }
    var refreshing by remember { mutableStateOf(false) }
    var expanded by remember { mutableStateOf(DesktopPreferences.usageExpanded()) }
    var nowMillis by remember { mutableLongStateOf(System.currentTimeMillis()) }
    val scope = rememberCoroutineScope()

    // A failed poll keeps the last reading; the list already reports an unreachable gateway.
    LaunchedEffect(api) {
        while (true) {
            runCatching { api.usage() }.onSuccess { usage = it }
            nowMillis = System.currentTimeMillis()
            delay(USAGE_POLL_MS)
        }
    }

    val shown = usage?.takeIf { it.providers.isNotEmpty() || it.error != null }
    UsageCard(
        state = shown?.let {
            UsageCardUiState(
                current = it,
                nowMillis = nowMillis,
                expanded = expanded,
                refreshing = refreshing,
                fetchedAtText = it.fetchedAt?.let { fetchedAt -> "updated " + updatedAgo(fetchedAt, nowMillis) },
            )
        },
        onAction = { action ->
            when (action) {
                UsageCardAction.Toggle -> {
                    expanded = !expanded
                    DesktopPreferences.saveUsageExpanded(expanded)
                }
                UsageCardAction.Refresh -> if (!refreshing) {
                    scope.launch {
                        refreshing = true
                        runCatching { api.refreshUsage() }.onSuccess {
                            usage = it
                            nowMillis = System.currentTimeMillis()
                        }
                        refreshing = false
                    }
                }
            }
        },
    )
}

private fun updatedAgo(iso: String, nowMillis: Long): String {
    val then = runCatching { Instant.parse(iso) }.getOrNull() ?: return ""
    val seconds = Duration.between(then, Instant.ofEpochMilli(nowMillis)).seconds.coerceAtLeast(0)
    return when {
        seconds < 60 -> "just now"
        seconds < 3_600 -> "${seconds / 60}m ago"
        seconds < 86_400 -> "${seconds / 3_600}h ago"
        else -> "${seconds / 86_400}d ago"
    }
}
