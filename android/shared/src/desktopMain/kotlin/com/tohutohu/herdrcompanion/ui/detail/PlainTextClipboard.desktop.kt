package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.ui.platform.ClipEntry
import androidx.compose.ui.ExperimentalComposeUiApi
import java.awt.datatransfer.StringSelection

@OptIn(ExperimentalComposeUiApi::class)
internal actual fun plainTextClipEntry(text: String): ClipEntry = ClipEntry(StringSelection(text))
