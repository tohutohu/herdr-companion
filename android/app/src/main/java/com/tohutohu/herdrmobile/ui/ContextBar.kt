package com.tohutohu.herdrmobile.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

/**
 * How full the session's context window is. Renders nothing until the agent
 * has reported a token count, which it does once a turn has run.
 */
@Composable
fun ContextBar(usedTokens: Long?, windowTokens: Long?, usedPercent: Int?, modifier: Modifier = Modifier) {
    val percent = usedPercent ?: return
    val fill = contextFill(usedTokens, windowTokens) ?: return
    Row(
        modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            "Context",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.primary,
            modifier = Modifier.width(56.dp),
        )
        LinearProgressIndicator(
            progress = { percent / 100f },
            color = contextColor(percent),
            modifier = Modifier.weight(1f).height(6.dp),
        )
        Text(" $percent%", style = MaterialTheme.typography.labelSmall, modifier = Modifier.width(44.dp))
        Text(
            fill,
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
    HorizontalDivider()
}

/**
 * The same reading as [ContextBar] squeezed into an app bar action: a ring
 * filled to the used share with the percentage in the middle. Tapping it is
 * what opens the full bar, so it renders nothing while there is nothing to
 * open either.
 */
@Composable
fun ContextGauge(usedPercent: Int?, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val percent = usedPercent ?: return
    val color = contextColor(percent)
    IconButton(
        onClick = onClick,
        modifier = modifier.semantics { contentDescription = "Context $percent%" },
    ) {
        Box(Modifier.size(28.dp), contentAlignment = Alignment.Center) {
            CircularProgressIndicator(
                progress = { percent / 100f },
                color = color,
                trackColor = MaterialTheme.colorScheme.surfaceVariant,
                strokeWidth = 3.dp,
                strokeCap = StrokeCap.Round,
                gapSize = 0.dp,
                modifier = Modifier.fillMaxSize(),
            )
            Text(
                "$percent",
                style = MaterialTheme.typography.labelSmall.copy(fontSize = 10.sp),
                color = color,
            )
        }
    }
}

/** Same thresholds as the subscription limits, so a full bar reads the same. */
@Composable
private fun contextColor(percent: Int): Color = when {
    percent >= 90 -> MaterialTheme.colorScheme.error
    percent >= 70 -> MaterialTheme.colorScheme.tertiary
    else -> MaterialTheme.colorScheme.primary
}
