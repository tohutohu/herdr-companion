package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.AttachFile
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Image
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import coil3.compose.AsyncImage
import com.tohutohu.herdrcompanion.ui.ExpandingContent

/** The picked attachments above a prompt field, each with a remove button. */
@Composable
fun AttachmentPreviewRow(
    attachments: List<AttachmentUiState>,
    resolvePreview: (String) -> Any?,
    onRemove: (String) -> Unit,
) {
    ExpandingContent(value = attachments.takeIf { it.isNotEmpty() }) { shown ->
        LazyRow(horizontalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.padding(bottom = 8.dp)) {
            items(shown, key = { it.id }) { attachment ->
                Box(Modifier.animateItem()) {
                    if (attachment.isImage) {
                        AsyncImage(
                            model = resolvePreview(attachment.id),
                            contentDescription = null,
                            modifier = Modifier.size(64.dp),
                        )
                    } else {
                        FileChip(attachment.name)
                    }
                    IconButton(
                        onClick = { onRemove(attachment.id) },
                        modifier = Modifier.size(24.dp).align(Alignment.TopEnd),
                    ) {
                        Icon(Icons.Default.Close, contentDescription = "Remove")
                    }
                }
            }
        }
    }
}

/** A "+" button that offers to attach an image or a file. */
@Composable
fun AttachButton(
    enabled: Boolean,
    onPickImage: () -> Unit,
    onPickFile: () -> Unit,
) {
    Box {
        var menuOpen by remember { mutableStateOf(false) }
        IconButton(enabled = enabled, onClick = { menuOpen = true }) {
            Icon(Icons.Default.Add, contentDescription = "Attach")
        }
        DropdownMenu(expanded = menuOpen && enabled, onDismissRequest = { menuOpen = false }) {
            DropdownMenuItem(
                text = { Text("Image") },
                leadingIcon = { Icon(Icons.Default.Image, contentDescription = null) },
                onClick = {
                    menuOpen = false
                    onPickImage()
                },
            )
            DropdownMenuItem(
                text = { Text("File") },
                leadingIcon = { Icon(Icons.Default.AttachFile, contentDescription = null) },
                onClick = {
                    menuOpen = false
                    onPickFile()
                },
            )
        }
    }
}

@Composable
private fun FileChip(name: String) {
    Surface(
        color = MaterialTheme.colorScheme.surfaceVariant,
        shape = MaterialTheme.shapes.small,
        modifier = Modifier.height(64.dp).widthIn(max = 160.dp),
    ) {
        Row(
            Modifier.padding(horizontal = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            Icon(Icons.Default.AttachFile, contentDescription = null, modifier = Modifier.size(16.dp))
            Text(name, style = MaterialTheme.typography.labelSmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
        }
    }
}
