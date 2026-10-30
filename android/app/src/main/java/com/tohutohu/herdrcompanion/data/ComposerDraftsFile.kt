package com.tohutohu.herdrcompanion.data

import com.tohutohu.herdrcompanion.ui.detail.ComposerDrafts
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.plus
import java.io.File

/** Unsent composer text in a private file, so a draft outlives the process. */
fun fileComposerDrafts(file: File, scope: CoroutineScope): ComposerDrafts = ComposerDrafts(
    initial = ComposerDrafts.decode(runCatching { file.readText() }.getOrNull()),
    scope = scope + Dispatchers.IO,
) { drafts ->
    runCatching {
        if (drafts.isEmpty()) {
            file.delete()
        } else {
            file.parentFile?.mkdirs()
            val temp = File(file.parentFile, "${file.name}.tmp")
            temp.writeText(ComposerDrafts.encode(drafts))
            temp.renameTo(file)
        }
    }
}
