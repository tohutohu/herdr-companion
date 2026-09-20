package com.tohutohu.herdrmobile.ui.detail

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.InsertDriveFile
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.ErrorOutline
import androidx.compose.material.icons.filled.Schedule
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import coil3.compose.AsyncImage
import com.tohutohu.herdrmobile.data.SendState
import com.tohutohu.herdrmobile.ui.SwapContent
import com.tohutohu.herdrmobile.ui.markdown.MarkdownText

/**
 * A message sent from the app that the conversation does not have yet. It is
 * drawn as an outline of the bubble it will become, with where it is on its
 * way underneath; the real message takes its place once the agent has it.
 */
@Composable
fun PendingMessageItem(
    message: PendingMessageUiState,
    showRole: Boolean,
    resolveAttachmentPreview: (String) -> Any?,
    onRetry: () -> Unit,
    onDiscard: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val failed = message.state == SendState.FAILED
    Column(
        modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 2.dp),
        horizontalAlignment = Alignment.End,
    ) {
        if (showRole) {
            Text(
                "You",
                style = MaterialTheme.typography.labelMedium,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.primary,
                modifier = Modifier.padding(top = 12.dp, bottom = 2.dp),
            )
        }
        Surface(
            color = MaterialTheme.colorScheme.primaryContainer.copy(alpha = 0.35f),
            border = BorderStroke(
                1.dp,
                if (failed) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.primaryContainer,
            ),
            shape = RoundedCornerShape(12.dp),
            modifier = Modifier.widthIn(max = 320.dp),
        ) {
            Column(Modifier.padding(10.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                message.attachments.forEach { a ->
                    if (a.isImage) {
                        AsyncImage(
                            model = resolveAttachmentPreview(a.id),
                            contentDescription = a.name,
                            contentScale = ContentScale.Fit,
                            modifier = Modifier.heightIn(max = 160.dp).clip(RoundedCornerShape(8.dp)),
                        )
                    } else {
                        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                            Icon(Icons.AutoMirrored.Filled.InsertDriveFile, contentDescription = null, modifier = Modifier.size(16.dp))
                            Text(
                                a.name,
                                fontFamily = FontFamily.Monospace,
                                style = MaterialTheme.typography.labelSmall,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                            )
                        }
                    }
                }
                if (message.text.isNotBlank()) {
                    SelectionContainer { MarkdownText(message.text) }
                }
            }
        }
        SwapContent(message.state) { state ->
            when (state) {
                SendState.SENDING -> DeliveryLine(label = "Sending…") {
                    CircularProgressIndicator(Modifier.size(10.dp), strokeWidth = 1.5.dp)
                }
                // The gateway has handed it to the agent and finishes on its
                // own from here: this is the moment the screen may be left.
                SendState.ACCEPTED -> DeliveryLine(label = "Delivered · waiting for the agent", icon = Icons.Default.Check)
                SendState.FAILED -> Column(horizontalAlignment = Alignment.End) {
                    DeliveryLine(
                        label = "Not sent: ${message.error ?: "unknown error"}",
                        icon = Icons.Default.ErrorOutline,
                        color = MaterialTheme.colorScheme.error,
                    )
                    Row {
                        TextButton(onClick = onDiscard) { Text("Discard") }
                        TextButton(onClick = onRetry) { Text("Retry") }
                    }
                }
            }
        }
    }
}

/** Under a queued message: the agent has it but takes it after the current step. */
@Composable
fun QueuedLine() {
    DeliveryLine(label = "Queued · the agent takes it after the current step", icon = Icons.Default.Schedule)
}

@Composable
private fun DeliveryLine(
    label: String,
    icon: ImageVector? = null,
    color: Color = MaterialTheme.colorScheme.onSurfaceVariant,
    leading: (@Composable () -> Unit)? = null,
) {
    Row(
        Modifier.padding(top = 2.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        Box(Modifier.size(12.dp), contentAlignment = Alignment.Center) {
            when {
                leading != null -> leading()
                icon != null -> Icon(icon, contentDescription = null, tint = color, modifier = Modifier.size(12.dp))
            }
        }
        Text(label, style = MaterialTheme.typography.labelSmall, color = color, maxLines = 2, overflow = TextOverflow.Ellipsis)
    }
}
