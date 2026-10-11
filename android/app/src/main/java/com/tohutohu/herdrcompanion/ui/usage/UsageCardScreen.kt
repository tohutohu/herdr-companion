package com.tohutohu.herdrcompanion.ui.usage

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.foundation.clickable
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
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
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.data.api.UsageProviderDto
import com.tohutohu.herdrcompanion.data.api.UsageWindowDto
import com.tohutohu.herdrcompanion.ui.ExpandChevron
import com.tohutohu.herdrcompanion.ui.ExpandingContent
import com.tohutohu.herdrcompanion.ui.SwapContent

/** Pure rendering for the usage card. [UsageCardRoute] supplies state/effects. */
@Composable
fun UsageCard(
    state: UsageCardUiState?,
    onAction: (UsageCardAction) -> Unit,
    modifier: Modifier = Modifier,
) {
    ExpandingContent(value = state, modifier = modifier.fillMaxWidth()) { current ->
        UsageCardContent(current, onAction)
    }
}

@Composable
private fun UsageCardContent(state: UsageCardUiState, onAction: (UsageCardAction) -> Unit) {
    Column(Modifier.fillMaxWidth()) {
        Row(
            Modifier
                .fillMaxWidth()
                .clickable { onAction(UsageCardAction.Toggle) }
                .padding(start = 16.dp, end = 4.dp, top = 6.dp, bottom = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text("Limits", style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary)
            Text(
                "  " + usageHeadlineWithReset(state.current.providers, state.nowMillis).ifEmpty { "unavailable" },
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
            IconButton(onClick = { onAction(UsageCardAction.Refresh) }, modifier = Modifier.size(32.dp)) {
                SwapContent(state.refreshing) { busy ->
                    if (busy) {
                        CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
                    } else {
                        Icon(Icons.Default.Refresh, contentDescription = "Refresh limits", Modifier.size(18.dp))
                    }
                }
            }
            ExpandChevron(
                expanded = state.expanded,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.size(18.dp),
            )
        }
        AnimatedVisibility(
            visible = state.expanded,
            enter = expandVertically() + fadeIn(),
            exit = shrinkVertically() + fadeOut(),
        ) {
            Column(
                Modifier.fillMaxWidth().padding(start = 16.dp, end = 16.dp, bottom = 8.dp),
                verticalArrangement = Arrangement.spacedBy(4.dp),
            ) {
                state.current.providers.forEach { ProviderUsage(it, state.nowMillis) }
                state.current.error?.let {
                    Text(it, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.error)
                }
                state.fetchedAtText?.let {
                    Text(it, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
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
    val ideal = idealUsedPercent(window, nowMillis)
    Column(
        Modifier.fillMaxWidth().padding(vertical = 2.dp),
        verticalArrangement = Arrangement.spacedBy(2.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(
                windowTitle(window),
                style = MaterialTheme.typography.labelSmall,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
            Text("${window.usedPercent}%", style = MaterialTheme.typography.labelSmall)
            resetText(window, nowMillis)?.let {
                Text(
                    it,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        val progress by animateFloatAsState((window.usedPercent / 100f).coerceIn(0f, 1f), label = "usage")
        val color by animateColorAsState(usedColor(window.usedPercent), label = "usageColor")
        val markerColor = MaterialTheme.colorScheme.onSurface
        Box(Modifier.fillMaxWidth().height(12.dp), contentAlignment = Alignment.Center) {
            LinearProgressIndicator(
                progress = { progress },
                color = color,
                modifier = Modifier.fillMaxWidth().height(6.dp),
            )
            if (ideal != null) {
                Canvas(Modifier.fillMaxWidth().height(12.dp)) {
                    val stroke = 2.dp.toPx()
                    val x = (size.width * ideal / 100f).coerceIn(stroke / 2, size.width - stroke / 2)
                    drawLine(markerColor, Offset(x, 0f), Offset(x, size.height), strokeWidth = stroke)
                }
            }
        }
    }
}

@Composable
private fun usedColor(percent: Int): Color = when {
    percent >= 90 -> MaterialTheme.colorScheme.error
    percent >= 70 -> MaterialTheme.colorScheme.tertiary
    else -> MaterialTheme.colorScheme.primary
}
