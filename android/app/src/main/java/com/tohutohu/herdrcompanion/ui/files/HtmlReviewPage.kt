package com.tohutohu.herdrcompanion.ui.files

import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.ExpandLess
import androidx.compose.material.icons.filled.ExpandMore
import androidx.compose.material3.Button
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.navigationevent.NavigationEventInfo
import androidx.navigationevent.compose.NavigationBackHandler
import androidx.navigationevent.compose.rememberNavigationEventState

/**
 * The rendered page with comments on its elements: in [commentMode] a tap picks an element and a
 * tray below takes the comment. [onSend] hands the collected [comments] on.
 */
@Composable
internal fun HtmlReviewPage(
    sessionId: String,
    path: String,
    bottom: Dp,
    commentMode: Boolean,
    comments: List<PageComment>,
    onCommentsChange: (List<PageComment>) -> Unit,
    onCommentModeChange: (Boolean) -> Unit,
    onSend: () -> Unit,
) {
    val controller = remember(sessionId, path) { HtmlPageController() }
    var pick by remember(sessionId, path) { mutableStateOf<Pair<String, ElementPick>?>(null) }
    var body by rememberSaveable(sessionId, path) { mutableStateOf("") }
    LaunchedEffect(commentMode) {
        if (!commentMode) pick = null
    }
    val trayShown = commentMode || comments.isNotEmpty()

    Column(Modifier.fillMaxSize()) {
        Box(Modifier.weight(1f).fillMaxWidth()) {
            HtmlPageView(
                sessionId = sessionId,
                path = path,
                bottom = if (trayShown) 0.dp else bottom,
                commentMode = commentMode,
                comments = comments,
                controller = controller,
                onPick = { page, element -> pick = page to element },
            )
        }
        if (trayShown) {
            CommentTray(
                bottom = bottom,
                commentMode = commentMode,
                pick = pick?.second,
                body = body,
                onBodyChange = { body = it },
                comments = comments,
                onParent = controller::selectParent,
                onCancel = {
                    pick = null
                    controller.clearSelection()
                },
                onAdd = {
                    val (page, element) = pick ?: return@CommentTray
                    onCommentsChange(comments + PageComment(page, element, body.trim()))
                    body = ""
                    pick = null
                    controller.clearSelection()
                },
                onRemove = { index -> onCommentsChange(comments.filterIndexed { i, _ -> i != index }) },
                onSend = onSend,
            )
        }
    }
    // Back steps out of commenting before it goes back in the page or leaves it. Composed after
    // the page's own handler, so it is asked first.
    NavigationBackHandler(
        state = rememberNavigationEventState(currentInfo = NavigationEventInfo.None),
        isBackEnabled = commentMode,
        onBackCompleted = {
            if (pick != null) {
                pick = null
                controller.clearSelection()
            } else {
                onCommentModeChange(false)
            }
        },
    )
}

@Composable
private fun CommentTray(
    bottom: Dp,
    commentMode: Boolean,
    pick: ElementPick?,
    body: String,
    onBodyChange: (String) -> Unit,
    comments: List<PageComment>,
    onParent: () -> Unit,
    onCancel: () -> Unit,
    onAdd: () -> Unit,
    onRemove: (Int) -> Unit,
    onSend: () -> Unit,
) {
    var listOpen by rememberSaveable { mutableStateOf(false) }
    val focus = remember { FocusRequester() }
    LaunchedEffect(pick) {
        if (pick != null) runCatching { focus.requestFocus() }
    }
    Surface(tonalElevation = 3.dp, modifier = Modifier.fillMaxWidth()) {
        Column(
            Modifier
                .imePadding()
                .padding(bottom = bottom)
                .padding(horizontal = 12.dp, vertical = 8.dp)
                .animateContentSize(),
        ) {
            when {
                pick != null -> {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            Text(
                                pick.tag,
                                fontFamily = FontFamily.Monospace,
                                style = MaterialTheme.typography.labelMedium,
                                color = MaterialTheme.colorScheme.primary,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                            )
                            if (pick.text.isNotBlank()) {
                                Text(
                                    pick.text,
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    maxLines = 1,
                                    overflow = TextOverflow.Ellipsis,
                                )
                            }
                        }
                        TextButton(onClick = onParent, enabled = pick.canParent) { Text("Parent") }
                        IconButton(onClick = onCancel) { Icon(Icons.Default.Close, contentDescription = "Cancel") }
                    }
                    Row(verticalAlignment = Alignment.Bottom, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        OutlinedTextField(
                            value = body,
                            onValueChange = onBodyChange,
                            placeholder = { Text("Comment on this element") },
                            minLines = 1,
                            maxLines = 5,
                            modifier = Modifier.weight(1f).focusRequester(focus),
                        )
                        Button(onClick = onAdd, enabled = body.isNotBlank()) { Text("Add") }
                    }
                }
                commentMode -> Text(
                    "Tap an element to comment on it",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(vertical = 8.dp),
                )
            }
            if (comments.isNotEmpty()) {
                if (pick != null || commentMode) HorizontalDivider(Modifier.padding(vertical = 6.dp))
                Row(verticalAlignment = Alignment.CenterVertically) {
                    TextButton(onClick = { listOpen = !listOpen }) {
                        Text(if (comments.size == 1) "1 comment" else "${comments.size} comments")
                        Icon(
                            if (listOpen) Icons.Default.ExpandLess else Icons.Default.ExpandMore,
                            contentDescription = null,
                        )
                    }
                    Box(Modifier.weight(1f))
                    Button(onClick = onSend) { Text("Add to message") }
                }
                if (listOpen) {
                    LazyColumn(Modifier.heightIn(max = 240.dp)) {
                        itemsIndexed(comments) { i, c ->
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                Column(Modifier.weight(1f).padding(vertical = 4.dp)) {
                                    Text(
                                        "${i + 1}. ${c.element.tag} ${c.element.text}",
                                        fontFamily = FontFamily.Monospace,
                                        style = MaterialTheme.typography.labelSmall,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        maxLines = 1,
                                        overflow = TextOverflow.Ellipsis,
                                    )
                                    Text(c.body, style = MaterialTheme.typography.bodyMedium, maxLines = 3, overflow = TextOverflow.Ellipsis)
                                }
                                IconButton(onClick = { onRemove(i) }) {
                                    Icon(Icons.Default.Close, contentDescription = "Remove comment ${i + 1}")
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}
