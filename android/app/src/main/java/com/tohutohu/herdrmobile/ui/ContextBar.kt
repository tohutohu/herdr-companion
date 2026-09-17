package com.tohutohu.herdrmobile.ui

import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp

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

/** Same thresholds as the subscription limits, so a full bar reads the same. */
@Composable
private fun contextColor(percent: Int): Color = when {
    percent >= 90 -> MaterialTheme.colorScheme.error
    percent >= 70 -> MaterialTheme.colorScheme.tertiary
    else -> MaterialTheme.colorScheme.primary
}
