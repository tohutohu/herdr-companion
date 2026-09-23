package com.tohutohu.herdrcompanion.desktop

import java.awt.FileDialog
import java.awt.Dialog
import java.awt.Frame
import java.awt.Window
import java.io.File
import java.io.FilenameFilter
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.Paths
import java.net.URLConnection
import javax.swing.JFileChooser
import javax.swing.filechooser.FileSystemView

data class DesktopAttachment(
    val path: Path,
    val name: String = path.fileName?.toString() ?: "file",
    val mimeType: String = guessMimeType(path),
    /** True only for files created by the Desktop client, such as pasted images. */
    val deleteWhenDone: Boolean = false,
) {
    val id: String get() = path.toAbsolutePath().normalize().toString()

    /** Coil on the JVM loads a File but has no fetcher for a java.net.URI. */
    val previewModel: Any get() = path.toFile()
}

/** Native-ish AWT file selection; the shared attachment pipeline sees only paths. */
object DesktopFilePicker {
    private val imageExtensions = setOf("png", "jpg", "jpeg", "gif", "webp", "heic", "bmp", "tiff")

    fun pickFiles(owner: Window, imagesOnly: Boolean = false, multiple: Boolean = true): List<Path> {
        val title = if (imagesOnly) "Choose images" else "Attach files"
        val dialog = when (owner) {
            is Frame -> FileDialog(owner, title, FileDialog.LOAD)
            is Dialog -> FileDialog(owner, title, FileDialog.LOAD)
            else -> FileDialog(null as Frame?, title, FileDialog.LOAD)
        }
        dialog.isMultipleMode = multiple
        if (imagesOnly) {
            dialog.filenameFilter = FilenameFilter { _: File?, name: String ->
                name.substringAfterLast('.', "").lowercase() in imageExtensions
            }
        }
        dialog.isVisible = true
        return dialog.files.orEmpty().map(File::toPath).filter(Files::isRegularFile)
    }

    /** JFileChooser is used only for directories because AWT FileDialog has no directory mode. */
    fun pickDirectory(owner: Window): Path? {
        val chooser = JFileChooser(FileSystemView.getFileSystemView().homeDirectory).apply {
            dialogTitle = "Choose working folder"
            fileSelectionMode = JFileChooser.DIRECTORIES_ONLY
            isMultiSelectionEnabled = false
        }
        return if (chooser.showOpenDialog(owner) == JFileChooser.APPROVE_OPTION) chooser.selectedFile?.toPath() else null
    }
}

fun Path.toDesktopAttachment(deleteWhenDone: Boolean = false): DesktopAttachment =
    DesktopAttachment(toAbsolutePath().normalize(), deleteWhenDone = deleteWhenDone)

fun guessMimeType(path: Path): String = runCatching {
    Files.probeContentType(path)
}.getOrNull()?.takeIf { it.isNotBlank() }
    ?: URLConnection.guessContentTypeFromName(path.fileName?.toString().orEmpty())
    ?: "application/octet-stream"
