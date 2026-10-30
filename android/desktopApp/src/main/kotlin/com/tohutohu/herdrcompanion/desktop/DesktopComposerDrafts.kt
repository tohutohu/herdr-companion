package com.tohutohu.herdrcompanion.desktop

import com.tohutohu.herdrcompanion.ui.detail.ComposerDrafts
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.Paths
import java.nio.file.StandardCopyOption

/**
 * Unsent composer text per session, shared by the panes and detached windows.
 * It lives in a file rather than the preferences, which cap a value at 8 KiB.
 */
internal val desktopComposerDrafts: ComposerDrafts by lazy {
    fileComposerDrafts(desktopStateDirectory(Paths.get(System.getProperty("user.home") ?: ".")).resolve("composer-drafts.json"))
}

internal fun fileComposerDrafts(
    file: Path,
    scope: CoroutineScope = CoroutineScope(SupervisorJob() + Dispatchers.IO),
): ComposerDrafts = ComposerDrafts(
    initial = ComposerDrafts.decode(runCatching { Files.readString(file) }.getOrNull()),
    scope = scope,
) { drafts ->
    runCatching {
        if (drafts.isEmpty()) {
            Files.deleteIfExists(file)
        } else {
            Files.createDirectories(file.parent)
            val temp = file.resolveSibling("${file.fileName}.tmp")
            Files.writeString(temp, ComposerDrafts.encode(drafts))
            Files.move(temp, file, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE)
        }
    }
}
