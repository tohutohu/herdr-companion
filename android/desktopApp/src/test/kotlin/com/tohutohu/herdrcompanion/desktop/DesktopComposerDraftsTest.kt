package com.tohutohu.herdrcompanion.desktop

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse

class DesktopComposerDraftsTest {
    private fun waitUntil(condition: () -> Boolean) {
        val deadline = System.currentTimeMillis() + 5_000
        while (!condition()) {
            check(System.currentTimeMillis() < deadline) { "timed out" }
            Thread.sleep(20)
        }
    }

    @Test
    fun 下書きはファイルに書かれ次の起動で読み戻せる() {
        val file: Path = Files.createTempDirectory("composer-drafts").resolve("state/composer-drafts.json")
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        try {
            val drafts = fileComposerDrafts(file, scope)
            drafts["claude:a"] = "途中"
            drafts["claude:a"] = "途中まで書いた"
            waitUntil { Files.exists(file) && fileComposerDrafts(file, CoroutineScope(SupervisorJob()))["claude:a"] == "途中まで書いた" }

            drafts["claude:a"] = ""
            waitUntil { !Files.exists(file) }
            assertEquals("", fileComposerDrafts(file, CoroutineScope(SupervisorJob()))["claude:a"])
            assertFalse(Files.exists(file.resolveSibling("composer-drafts.json.tmp")))
        } finally {
            scope.cancel()
        }
    }
}
