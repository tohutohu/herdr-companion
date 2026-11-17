package com.tohutohu.herdrcompanion.ui.input

import androidx.compose.ui.platform.ClipEntry

internal actual fun ClipEntry.readTextAndHtml(): Pair<String?, String?> {
    val item = clipData.takeIf { it.itemCount > 0 }?.getItemAt(0) ?: return null to null
    return item.text?.toString() to item.htmlText
}
