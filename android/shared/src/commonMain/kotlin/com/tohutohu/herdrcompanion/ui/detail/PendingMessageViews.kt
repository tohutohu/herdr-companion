package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
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
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import coil3.compose.AsyncImage
import com.tohutohu.herdrcompanion.data.SendState
import com.tohutohu.herdrcompanion.ui.ExpandingContent
import com.tohutohu.herdrcompanion.ui.SwapContent
import com.tohutohu.herdrcompanion.ui.UiMotion
import com.tohutohu.herdrcompanion.ui.markdown.MarkdownText

/** How far a message from the reader has got, shown under its bubble. */
sealed interface Delivery {
    data object Sending : Delivery

    /** The gateway handed it to the agent, which has not taken it in yet. */
    data object Accepted : Delivery

    data class Failed(val error: String?, val canRetry: Boolean) : Delivery

    /** In the conversation, but the agent takes it after the current step. */
    data object Queued : Delivery
}

val PendingMessageUiState.delivery: Delivery
    get() = when (state) {
        SendState.SENDING -> Delivery.Sending
        SendState.ACCEPTED -> Delivery.Accepted
        SendState.FAILED -> Delivery.Failed(error, canRetry)
    }

/**
 * The reader's message, from the moment it is sent until the conversation has
 * it. It is one element all the way: an outline of the bubble while it is on
 * its way, which fills in once the agent has it, with how far it got shown
 * underneath until then. Callers keep it in one place in the composition (and
 * the list keeps its key) so it changes in place instead of being replaced.
 */
@Composable
fun UserBubble(
    pending: Boolean,
    delivery: Delivery?,
    modifier: Modifier = Modifier,
    onRetry: () -> Unit = {},
    onDiscard: () -> Unit = {},
    content: @Composable () -> Unit,
) {
    val scheme = MaterialTheme.colorScheme
    val fill by animateColorAsState(
        if (pending) scheme.surfaceContainerHighest.copy(alpha = 0.35f) else scheme.surfaceContainerHighest,
        UiMotion.resize(),
        label = "bubbleFill",
    )
    val edge by animateColorAsState(
        if (delivery is Delivery.Failed) scheme.error else scheme.surfaceContainerHighest,
        UiMotion.resize(),
        label = "bubbleEdge",
    )
    Column(
        modifier.fillMaxWidth().padding(start = 12.dp, end = 12.dp, top = 14.dp, bottom = 2.dp),
        horizontalAlignment = Alignment.End,
    ) {
        Surface(
            color = fill,
            contentColor = scheme.onSurface,
            border = BorderStroke(1.dp, edge),
            shape = UserBubbleShape,
            modifier = Modifier.userBubbleWidth(),
        ) {
            Box(Modifier.animateContentSize(UiMotion.resize()).padding(horizontal = 14.dp, vertical = 10.dp)) { content() }
        }
        ExpandingContent(delivery) { d ->
            SwapContent(d) { DeliveryFooter(it, onRetry, onDiscard) }
        }
    }
}

/**
 * A message sent from the app that the conversation does not have yet. The
 * message list draws it with [UserBubble] and [PendingMessageBody] itself, so
 * the bubble lives on as the message once it arrives.
 */
@Composable
fun PendingMessageItem(
    message: PendingMessageUiState,
    resolveAttachmentPreview: (String) -> Any?,
    onRetry: () -> Unit,
    onDiscard: () -> Unit,
    modifier: Modifier = Modifier,
) {
    UserBubble(pending = true, delivery = message.delivery, modifier = modifier, onRetry = onRetry, onDiscard = onDiscard) {
        PendingMessageBody(message, resolveAttachmentPreview)
    }
}

/** What was sent, in the order and sizes of the message it becomes. */
@Composable
fun PendingMessageBody(message: PendingMessageUiState, resolveAttachmentPreview: (String) -> Any?) {
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        // The agents take the text first and the images after it.
        if (message.text.isNotBlank()) {
            SelectionContainer { MarkdownText(message.text) }
        }
        message.attachments.forEach { a ->
            if (a.isImage) {
                AsyncImage(
                    model = resolveAttachmentPreview(a.id),
                    contentDescription = a.name,
                    contentScale = ContentScale.Fit,
                    modifier = Modifier.heightIn(max = 240.dp).clip(RoundedCornerShape(8.dp)),
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
    }
}

@Composable
private fun DeliveryFooter(delivery: Delivery, onRetry: () -> Unit, onDiscard: () -> Unit) {
    when (delivery) {
        Delivery.Sending -> DeliveryLine(label = "Sending…") {
            CircularProgressIndicator(Modifier.size(10.dp), strokeWidth = 1.5.dp)
        }
        // The gateway has handed it to the agent and finishes on its own from
        // here: this is the moment the screen may be left.
        Delivery.Accepted -> DeliveryLine(label = "Delivered · waiting for the agent", icon = Icons.Default.Check)
        is Delivery.Failed -> Column(horizontalAlignment = Alignment.End) {
            DeliveryLine(
                label = if (delivery.canRetry) "Not sent: ${delivery.error ?: "unknown error"}"
                else delivery.error ?: "Delivery could not be confirmed. Check the conversation before sending again.",
                icon = Icons.Default.ErrorOutline,
                color = MaterialTheme.colorScheme.error,
            )
            Row {
                TextButton(onClick = onDiscard) { Text("Discard") }
                TextButton(onClick = onRetry, enabled = delivery.canRetry) { Text("Retry") }
            }
        }
        Delivery.Queued -> DeliveryLine(label = "Queued · the agent takes it after the current step", icon = Icons.Default.Schedule)
    }
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
