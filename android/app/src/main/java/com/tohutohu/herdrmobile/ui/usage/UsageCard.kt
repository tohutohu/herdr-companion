package com.tohutohu.herdrmobile.ui.usage

import android.text.format.DateUtils
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import com.tohutohu.herdrmobile.container
import com.tohutohu.herdrmobile.data.SessionRepository
import com.tohutohu.herdrmobile.data.api.UsageDto
import com.tohutohu.herdrmobile.data.api.UsageProviderDto
import com.tohutohu.herdrmobile.data.api.UsageWindowDto
import com.tohutohu.herdrmobile.ui.ExpandChevron
import com.tohutohu.herdrmobile.ui.ExpandingContent
import com.tohutohu.herdrmobile.ui.SwapContent
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/** The gateway serves a cached reading, so polling it is cheap. */
private const val USAGE_POLL_MS = 60_000L

/**
 * How much of the Claude / Codex plan limits is used. Collapsed it is a single
 * line; tapping it shows every window with its reset time. The refresh button
 * makes the gateway read the limits again instead of serving its cache.
 */
@Composable
fun UsageCard(modifier: Modifier = Modifier) {
    val api = LocalContext.current.container.api
    var usage by remember { mutableStateOf<UsageDto?>(null) }
    var refreshing by remember { mutableStateOf(false) }
    var expanded by rememberSaveable { mutableStateOf(false) }
    var nowMillis by remember { mutableStateOf(System.currentTimeMillis()) }
    val scope = rememberCoroutineScope()
    val lifecycle = LocalLifecycleOwner.current.lifecycle

    // A failed poll keeps the last reading on screen; the session list already
    // reports an unreachable gateway.
    LaunchedEffect(lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) {
                runCatching { api.usage() }.onSuccess { usage = it }
                delay(USAGE_POLL_MS)
            }
        }
    }

    // Reset labels should continue updating while the card is visible even
    // when the gateway returns the same cached usage snapshot.
    LaunchedEffect(lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) {
                nowMillis = System.currentTimeMillis()
                delay(USAGE_POLL_MS)
            }
        }
    }

    // Grows in with the first reading instead of pushing the list down at once.
    val shown = usage?.takeIf { it.providers.isNotEmpty() || it.error != null }
    ExpandingContent(value = shown, modifier = modifier.fillMaxWidth()) { current ->
        UsageCardContent(
            current = current,
            nowMillis = nowMillis,
            expanded = expanded,
            refreshing = refreshing,
            onToggle = { expanded = !expanded },
            onRefresh = {
                if (!refreshing) {
                    scope.launch {
                        refreshing = true
                        runCatching { api.refreshUsage() }.onSuccess { usage = it }
                        refreshing = false
                    }
                }
            },
        )
    }
}

@Composable
private fun UsageCardContent(
    current: UsageDto,
    nowMillis: Long,
    expanded: Boolean,
    refreshing: Boolean,
    onToggle: () -> Unit,
    onRefresh: () -> Unit,
) {
    Column(Modifier.fillMaxWidth()) {
        Row(
            Modifier
                .fillMaxWidth()
                .clickable(onClick = onToggle)
                .padding(start = 16.dp, end = 4.dp, top = 6.dp, bottom = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text("Limits", style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary)
            Text(
                "  " + usageHeadlineWithReset(current.providers, nowMillis).ifEmpty { "unavailable" },
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
            IconButton(onClick = onRefresh, modifier = Modifier.size(32.dp)) {
                SwapContent(refreshing) { busy ->
                    if (busy) {
                        CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
                    } else {
                        Icon(Icons.Default.Refresh, contentDescription = "Refresh limits", Modifier.size(18.dp))
                    }
                }
            }
            ExpandChevron(
                expanded = expanded,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.size(18.dp),
            )
        }
        AnimatedVisibility(
            visible = expanded,
            enter = expandVertically() + fadeIn(),
            exit = shrinkVertically() + fadeOut(),
        ) {
            Column(
                Modifier.fillMaxWidth().padding(start = 16.dp, end = 16.dp, bottom = 8.dp),
                verticalArrangement = Arrangement.spacedBy(4.dp),
            ) {
                current.providers.forEach { ProviderUsage(it, nowMillis) }
                current.error?.let {
                    Text(it, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.error)
                }
                current.fetchedAt?.let {
                    Text(
                        "updated " + DateUtils.getRelativeTimeSpanString(SessionRepository.parseTime(it)),
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
        HorizontalDivider()
    }
}

@Composable
private fun ProviderUsage(provider: UsageProviderDto, nowMillis: Long) {
    Text(
        provider.displayName + (provider.plan?.takeIf { it.isNotBlank() }?.let { " · $it" } ?: ""),
        style = MaterialTheme.typography.labelMedium,
        color = MaterialTheme.colorScheme.primary,
    )
    provider.error?.takeIf { it.isNotBlank() }?.let {
        Text(it, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.error)
    }
    provider.windows.forEach { WindowRow(it, nowMillis) }
}

@Composable
private fun WindowRow(window: UsageWindowDto, nowMillis: Long) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Text(
            windowTitle(window),
            style = MaterialTheme.typography.labelSmall,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.width(110.dp),
        )
        val progress by animateFloatAsState(window.usedPercent / 100f, label = "usage")
        val color by animateColorAsState(usedColor(window.usedPercent), label = "usageColor")
        LinearProgressIndicator(
            progress = { progress },
            color = color,
            modifier = Modifier.weight(1f).height(6.dp),
        )
        Text(
            " ${window.usedPercent}%",
            style = MaterialTheme.typography.labelSmall,
            modifier = Modifier.width(44.dp),
        )
        Text(
            resetText(window, nowMillis).orEmpty(),
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.width(88.dp),
        )
    }
}

@Composable
private fun usedColor(percent: Int): Color = when {
    percent >= 90 -> MaterialTheme.colorScheme.error
    percent >= 70 -> MaterialTheme.colorScheme.tertiary
    else -> MaterialTheme.colorScheme.primary
}
