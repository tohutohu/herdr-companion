package com.tohutohu.herdrcompanion.ui.usage

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
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
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.data.api.UsageProviderDto
import com.tohutohu.herdrcompanion.data.api.UsageWindowDto
import com.tohutohu.herdrcompanion.ui.ExpandChevron
import com.tohutohu.herdrcompanion.ui.ExpandingContent
import com.tohutohu.herdrcompanion.ui.SwapContent

/** Windows per row in the expanded card; Claude has three, the others two or three. */
private const val WINDOWS_PER_ROW = 3

/**
 * How much of the plan limits is used. Collapsed it is one or two lines with each provider's
 * tightest window; expanded it is a compact grid, one row per provider, so it does not push the
 * list off the screen. The platform route supplies the state and effects.
 */
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
                .padding(start = 16.dp, end = 4.dp, top = 4.dp, bottom = 4.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                "Limits",
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.primary,
                modifier = Modifier.padding(end = 12.dp),
            )
            Box(Modifier.weight(1f)) {
                if (state.expanded) {
                    state.fetchedAtText?.let {
                        Text(
                            it,
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            maxLines = 1,
                        )
                    }
                } else {
                    UsageSummary(state)
                }
            }
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
                contentDescription = if (state.expanded) "Collapse limits" else "Expand limits",
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(end = 8.dp).size(18.dp),
            )
        }
        AnimatedVisibility(
            visible = state.expanded,
            enter = expandVertically() + fadeIn(),
            exit = shrinkVertically() + fadeOut(),
        ) {
            Column(
                Modifier.fillMaxWidth().padding(start = 16.dp, end = 16.dp, bottom = 8.dp),
                verticalArrangement = Arrangement.spacedBy(6.dp),
            ) {
                state.current.providers.forEach { ProviderUsage(it, state.nowMillis) }
                state.current.error?.let {
                    Text(it, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.error)
                }
            }
        }
        HorizontalDivider()
    }
}

/** "Claude 37% 11/28 10:00   Codex 5% 11/24 22:05 …", wrapping between providers rather than inside one. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun UsageSummary(state: UsageCardUiState) {
    val providers = state.current.providers.mapNotNull { p -> tightestWindow(p)?.let { p to it } }
    if (providers.isEmpty()) {
        Text(
            "unavailable",
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        return
    }
    val muted = MaterialTheme.colorScheme.onSurfaceVariant
    val description = usageHeadlineWithReset(state.current.providers, state.nowMillis)
    FlowRow(
        horizontalArrangement = Arrangement.spacedBy(12.dp),
        modifier = Modifier.clearAndSetSemantics { contentDescription = description },
    ) {
        providers.forEach { (provider, window) ->
            val percentColor = usedColor(window.usedPercent)
            val reset = resetText(window, state.nowMillis)
            Text(
                buildAnnotatedString {
                    withStyle(SpanStyle(color = MaterialTheme.colorScheme.onSurface)) { append(provider.displayName) }
                    append(" ")
                    withStyle(SpanStyle(color = percentColor, fontWeight = FontWeight.SemiBold)) {
                        append("${window.usedPercent}%")
                    }
                    reset?.let { withStyle(SpanStyle(color = muted)) { append(" $it") } }
                },
                style = MaterialTheme.typography.labelMedium,
                maxLines = 1,
            )
        }
    }
}

@Composable
private fun ProviderUsage(provider: UsageProviderDto, nowMillis: Long) {
    Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
        Text(
            provider.displayName + (provider.plan?.takeIf { it.isNotBlank() }?.let { " · $it" } ?: ""),
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.primary,
        )
        provider.error?.takeIf { it.isNotBlank() }?.let {
            Text(it, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.error)
        }
        provider.windows.chunked(WINDOWS_PER_ROW).forEach { row ->
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                row.forEach { WindowCell(it, nowMillis, Modifier.weight(1f)) }
                // Keeps the columns of every provider aligned when one has fewer windows.
                repeat(WINDOWS_PER_ROW - row.size) { Spacer(Modifier.weight(1f)) }
            }
        }
    }
}

/** Title, a thin bar with the ideal-pace marker, then the percentage and reset. */
@Composable
private fun WindowCell(window: UsageWindowDto, nowMillis: Long, modifier: Modifier = Modifier) {
    val ideal = idealUsedPercent(window, nowMillis)
    val color by animateColorAsState(usedColor(window.usedPercent), label = "usageColor")
    Column(modifier, verticalArrangement = Arrangement.spacedBy(1.dp)) {
        Text(
            windowTitle(window),
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        val progress by animateFloatAsState((window.usedPercent / 100f).coerceIn(0f, 1f), label = "usage")
        val markerColor = MaterialTheme.colorScheme.onSurface
        Box(Modifier.fillMaxWidth().height(10.dp), contentAlignment = Alignment.Center) {
            LinearProgressIndicator(
                progress = { progress },
                color = color,
                gapSize = 0.dp,
                drawStopIndicator = {},
                modifier = Modifier.fillMaxWidth().height(4.dp),
            )
            if (ideal != null) {
                Canvas(Modifier.fillMaxWidth().height(10.dp)) {
                    val stroke = 2.dp.toPx()
                    val x = (size.width * ideal / 100f).coerceIn(stroke / 2, size.width - stroke / 2)
                    drawLine(markerColor, Offset(x, 0f), Offset(x, size.height), strokeWidth = stroke)
                }
            }
        }
        Text(
            buildAnnotatedString {
                withStyle(SpanStyle(color = color, fontWeight = FontWeight.SemiBold)) { append("${window.usedPercent}%") }
                resetText(window, nowMillis)?.let {
                    withStyle(SpanStyle(color = MaterialTheme.colorScheme.onSurfaceVariant)) { append("  $it") }
                }
            },
            style = MaterialTheme.typography.labelSmall,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

@Composable
private fun usedColor(percent: Int): Color = when {
    percent >= 90 -> MaterialTheme.colorScheme.error
    percent >= 70 -> MaterialTheme.colorScheme.tertiary
    else -> MaterialTheme.colorScheme.primary
}
