package com.tohutohu.herdrmobile.desktop

import androidx.compose.foundation.draganddrop.dragAndDropTarget
import androidx.compose.runtime.Composable
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Modifier
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.draganddrop.DragAndDropEvent
import androidx.compose.ui.draganddrop.DragAndDropTarget
import java.awt.datatransfer.DataFlavor
import java.awt.dnd.DropTargetDragEvent
import java.awt.dnd.DropTargetDropEvent
import java.io.File
import java.net.URI
import java.nio.file.Path
import java.nio.file.Paths

data class DesktopAttachmentDropState(
    val modifier: Modifier,
    val active: Boolean,
)

@Composable
fun rememberDesktopAttachmentDropTarget(
    enabled: Boolean,
    onFilesDropped: (List<Path>) -> Unit,
): DesktopAttachmentDropState {
    val active = remember { mutableStateOf(false) }
    val currentDropHandler = rememberUpdatedState(onFilesDropped)
    val target = remember { AttachmentDropTarget(active, { paths -> currentDropHandler.value(paths) }) }
    val modifier = if (enabled) {
        Modifier.dragAndDropTarget(
            shouldStartDragAndDrop = { event -> desktopDropFiles(event).isNotEmpty() },
            target = target,
        )
    } else {
        Modifier
    }
    return DesktopAttachmentDropState(modifier = modifier, active = active.value)
}

private class AttachmentDropTarget(
    private val active: MutableState<Boolean>,
    private val onDrop: (List<Path>) -> Unit,
) : DragAndDropTarget {
    override fun onStarted(event: DragAndDropEvent) {
        active.value = true
    }

    override fun onExited(event: DragAndDropEvent) {
        active.value = false
    }

    override fun onEnded(event: DragAndDropEvent) {
        active.value = false
    }

    override fun onDrop(event: DragAndDropEvent): Boolean {
        val paths = desktopDropFiles(event)
        active.value = false
        if (paths.isEmpty()) return false
        onDrop(paths)
        return true
    }
}

@OptIn(ExperimentalComposeUiApi::class)
private fun desktopDropFiles(event: DragAndDropEvent): List<Path> {
    val transferable = when (val native = event.nativeEvent) {
        is DropTargetDragEvent -> native.transferable
        is DropTargetDropEvent -> native.transferable
        else -> null
    } ?: return emptyList()
    if (!transferable.isDataFlavorSupported(DataFlavor.javaFileListFlavor)) return emptyList()
    val files = runCatching { transferable.getTransferData(DataFlavor.javaFileListFlavor) as? List<*> }
        .getOrNull()
        .orEmpty()
    return files.filterIsInstance<File>().map(File::toPath).filter { it.toFile().isFile }
}

/** Kept for tests and for Finder implementations that expose file:// URLs. */
internal fun fileUrlToPath(value: String): Path? = runCatching {
    val uri = URI(value)
    if (uri.scheme == null) Paths.get(value) else Paths.get(uri)
}.getOrNull()
