package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ExpandLess
import androidx.compose.material.icons.filled.ExpandMore
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.model.BackgroundTaskUiModel
import com.tohutohu.herdrcompanion.ui.BACKGROUND_SYMBOL
import com.tohutohu.herdrcompanion.ui.backgroundElapsed
import com.tohutohu.herdrcompanion.ui.backgroundKindLabel
import com.tohutohu.herdrcompanion.ui.backgroundLabel
import kotlin.time.Clock
import kotlinx.coroutines.delay

/**
 * The work a session left running, above the composer. Says "in background"
 * and how long, never that it is working: a dev server stays here for good.
 */
@Composable
internal fun BackgroundTasksBar(sessionId: String, tasks: List<BackgroundTaskUiModel>) {
    var open by rememberSaveable(sessionId) { mutableStateOf(false) }
    Column(Modifier.fillMaxWidth()) {
        HorizontalDivider()
        Row(
            Modifier
                .fillMaxWidth()
                .clickable { open = !open }
                .padding(horizontal = 16.dp, vertical = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                "$BACKGROUND_SYMBOL ${backgroundLabel(tasks.size)}",
                color = MaterialTheme.colorScheme.tertiary,
                style = MaterialTheme.typography.labelMedium,
                modifier = Modifier.weight(1f),
            )
            Icon(
                if (open) Icons.Default.ExpandLess else Icons.Default.ExpandMore,
                contentDescription = if (open) "Hide background tasks" else "Show background tasks",
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.size(18.dp),
            )
        }
        if (open) {
            val now by produceState(Clock.System.now()) {
                while (true) {
                    delay(ELAPSED_REFRESH_MS)
                    value = Clock.System.now()
                }
            }
            Column(
                Modifier.padding(start = 16.dp, end = 16.dp, bottom = 8.dp),
                verticalArrangement = Arrangement.spacedBy(4.dp),
            ) {
                tasks.forEach { task ->
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(
                            backgroundKindLabel(task.kind),
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            style = MaterialTheme.typography.labelSmall,
                        )
                        Spacer(Modifier.width(8.dp))
                        Text(
                            task.description ?: task.id,
                            style = MaterialTheme.typography.bodySmall,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.weight(1f),
                        )
                        backgroundElapsed(task.startedAt, now)?.let {
                            Spacer(Modifier.width(8.dp))
                            Text(
                                it,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                style = MaterialTheme.typography.labelSmall,
                            )
                        }
                    }
                }
            }
        }
    }
}

private const val ELAPSED_REFRESH_MS = 30_000L
