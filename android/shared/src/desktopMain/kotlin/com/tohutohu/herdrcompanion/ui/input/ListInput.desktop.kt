package com.tohutohu.herdrcompanion.ui.input

import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.platform.ClipEntry
import java.awt.datatransfer.DataFlavor
import java.awt.datatransfer.Transferable

private val HTML = DataFlavor("text/html;class=java.lang.String")

@OptIn(ExperimentalComposeUiApi::class)
internal actual fun ClipEntry.readTextAndHtml(): Pair<String?, String?> {
    val transferable = nativeClipEntry as? Transferable ?: return null to null
    fun read(flavor: DataFlavor): String? =
        if (transferable.isDataFlavorSupported(flavor)) runCatching { transferable.getTransferData(flavor) as? String }.getOrNull() else null
    return read(DataFlavor.stringFlavor) to read(HTML)
}
