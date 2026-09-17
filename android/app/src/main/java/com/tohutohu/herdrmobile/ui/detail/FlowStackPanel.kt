package com.tohutohu.herdrmobile.ui.detail

import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ExpandLess
import androidx.compose.material.icons.filled.ExpandMore
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

/**
 * Pinned over the top of the message list. Tapping an entry scrolls to its
 * message; the reports can be folded away to leave only the instruction.
 */
@Composable
fun FlowStackPanel(
    stack: FlowStack,
    providerName: String,
    onJump: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    if (stack.isEmpty) return
    var expanded by rememberSaveable { mutableStateOf(true) }
    Surface(tonalElevation = 3.dp, shadowElevation = 4.dp, modifier = modifier.fillMaxWidth().animateContentSize()) {
        Column(Modifier.padding(vertical = 4.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    stack.instruction?.let { FlowRow("You", it, maxLines = 2, onJump) }
                }
                if (stack.reports.isNotEmpty()) {
                    IconButton(onClick = { expanded = !expanded }, modifier = Modifier.size(36.dp)) {
                        Icon(
                            if (expanded) Icons.Default.ExpandLess else Icons.Default.ExpandMore,
                            contentDescription = if (expanded) "Hide reports" else "Show ${stack.reports.size} reports",
                        )
                    }
                }
            }
            if (expanded && stack.reports.isNotEmpty()) {
                val scroll = rememberScrollState()
                // Keep the newest report in view; older ones scroll inside the panel.
                LaunchedEffect(stack.reports.size) { scroll.scrollTo(Int.MAX_VALUE) }
                Column(Modifier.heightIn(max = 176.dp).verticalScroll(scroll)) {
                    stack.reports.forEach { FlowRow(providerName.substringBefore(' '), it, maxLines = 2, onJump) }
                }
            }
        }
    }
}

@Composable
private fun FlowRow(label: String, entry: FlowEntry, maxLines: Int, onJump: (String) -> Unit) {
    Row(
        Modifier
            .fillMaxWidth()
            .clickable { onJump(entry.messageId) }
            .padding(horizontal = 12.dp, vertical = 3.dp),
    ) {
        Text(
            label,
            style = MaterialTheme.typography.labelMedium,
            fontWeight = FontWeight.Bold,
            color = MaterialTheme.colorScheme.primary,
            maxLines = 1,
            modifier = Modifier.width(48.dp).padding(top = 1.dp),
        )
        Text(
            entry.text,
            style = MaterialTheme.typography.bodySmall,
            maxLines = maxLines,
            overflow = TextOverflow.Ellipsis,
        )
    }
}
