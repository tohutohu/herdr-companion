package com.tohutohu.herdrcompanion.desktop

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.draganddrop.dragAndDropTarget
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.State
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Alignment
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.Modifier
import androidx.compose.ui.draganddrop.DragAndDropEvent
import androidx.compose.ui.draganddrop.DragAndDropTarget
import androidx.compose.ui.unit.dp
import java.awt.datatransfer.DataFlavor
import java.awt.datatransfer.Transferable
import java.awt.dnd.DropTargetDragEvent
import java.awt.dnd.DropTargetDropEvent
import java.io.File
import java.nio.file.Files
import java.nio.file.Path

data class DesktopAttachmentDropState(
    val modifier: Modifier,
    val active: Boolean,
)

/**
 * The modifier stays attached while disabled: removing the node in the middle of
 * a drag would skip the end callback and leave the overlay showing.
 */
@Composable
fun rememberDesktopAttachmentDropTarget(
    enabled: Boolean,
    onFilesDropped: (List<Path>) -> Unit,
): DesktopAttachmentDropState {
    val active = remember { mutableStateOf(false) }
    val currentEnabled = rememberUpdatedState(enabled)
    val currentDropHandler = rememberUpdatedState(onFilesDropped)
    val target = remember {
        AttachmentDropTarget(active, currentEnabled) { paths -> currentDropHandler.value(paths) }
    }
    val modifier = Modifier.dragAndDropTarget(
        shouldStartDragAndDrop = { event -> currentEnabled.value && desktopDropFiles(event).isNotEmpty() },
        target = target,
    )
    return DesktopAttachmentDropState(modifier = modifier, active = active.value && enabled)
}

/** Covers the drop area while files are held over it, so it is clear where they will go. */
@Composable
fun BoxScope.DesktopAttachmentDropOverlay(active: Boolean) {
    if (!active) return
    val colors = MaterialTheme.colorScheme
    Box(
        Modifier.matchParentSize()
            .background(colors.primaryContainer.copy(alpha = 0.85f))
            .border(2.dp, colors.primary),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            "Drop files to attach",
            color = colors.onPrimaryContainer,
            style = MaterialTheme.typography.titleMedium,
        )
    }
}

private class AttachmentDropTarget(
    private val active: MutableState<Boolean>,
    private val enabled: State<Boolean>,
    private val onDrop: (List<Path>) -> Unit,
) : DragAndDropTarget {
    // Not onStarted: that reaches every target in the window as soon as the drag
    // enters it, which would light up all the split panes.
    override fun onEntered(event: DragAndDropEvent) {
        active.value = true
    }

    override fun onExited(event: DragAndDropEvent) {
        active.value = false
    }

    override fun onEnded(event: DragAndDropEvent) {
        active.value = false
    }

    override fun onDrop(event: DragAndDropEvent): Boolean {
        active.value = false
        if (!enabled.value) return false
        val paths = desktopDropFiles(event)
        if (paths.isEmpty()) return false
        onDrop(paths)
        return true
    }
}

@OptIn(ExperimentalComposeUiApi::class)
private fun desktopDropFiles(event: DragAndDropEvent): List<Path> =
    when (val native = event.nativeEvent) {
        is DropTargetDragEvent -> native.transferable
        is DropTargetDropEvent -> native.transferable
        else -> null
    }?.droppedFiles().orEmpty()

/** Only regular files can be attached, so a folder held over the window is refused. */
internal fun Transferable.droppedFiles(): List<Path> {
    if (!isDataFlavorSupported(DataFlavor.javaFileListFlavor)) return emptyList()
    val files = runCatching { getTransferData(DataFlavor.javaFileListFlavor) as? List<*> }
        .getOrNull()
        .orEmpty()
    return files.filterIsInstance<File>().map(File::toPath).filter(Files::isRegularFile)
}
