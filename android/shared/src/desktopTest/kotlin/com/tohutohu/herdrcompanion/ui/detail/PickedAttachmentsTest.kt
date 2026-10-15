package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.LocalSaveableStateRegistry
import androidx.compose.runtime.saveable.SaveableStateRegistry
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.SnapshotStateList
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.runBlocking
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNotNull
import org.junit.Rule
import org.junit.Test

class PickedAttachmentsTest {
    @get:Rule val rule = createComposeRule()

    @Test
    fun 選択添付は画面再生成後も最小メタデータから復元され別セッションへ漏れない() {
        var visible by mutableStateOf(true)
        var owner by mutableStateOf("session-a")
        var registry by mutableStateOf(SaveableStateRegistry(null) { it is String || it is List<*> })
        lateinit var selection: SnapshotStateList<AttachmentUiState>
        val attachment = AttachmentUiState("content://picker/document/1", "report.pdf", "application/pdf", size = 42L)
        rule.setContent {
            if (visible) MaterialTheme {
                CompositionLocalProvider(LocalSaveableStateRegistry provides registry) {
                    selection = rememberPickedAttachments(owner)
                    Text(selection.joinToString { it.name })
                }
            }
        }
        rule.runOnIdle { selection += attachment }
        var saved: Map<String, List<Any?>> = emptyMap()
        rule.runOnIdle { saved = registry.performSave(); visible = false }
        rule.waitForIdle()
        rule.runOnIdle {
            registry = SaveableStateRegistry(saved) { it is String || it is List<*> }
            visible = true
        }
        rule.onNodeWithText("report.pdf").assertExists()
        rule.runOnIdle { assertEquals(listOf(attachment), selection.toList()); owner = "session-b" }
        rule.runOnIdle { assertEquals(emptyList(), selection.toList()) }
    }

    @Test
    fun 読めない添付が混じっても読み取れた添付を残し失敗を表示できる(): Unit = runBlocking {
        val result = readPickedAttachmentMetadata(listOf("ok", "revoked", "ok")) { id ->
            if (id == "revoked") throw SecurityException("permission expired")
            AttachmentUiState(id, "notes.txt", "text/plain")
        }
        assertEquals(listOf(AttachmentUiState("ok", "notes.txt", "text/plain")), result.attachments)
        assertEquals(1, result.failedCount)
        assertNotNull(result.error)
    }

    @Test
    fun サイズを保存していない旧形式もサイズ不明として復元できる() {
        val restored = PickedAttachmentsSaver.restore(listOf("content://document/old", "old.txt", "text/plain"))
        assertEquals(listOf(AttachmentUiState("content://document/old", "old.txt", "text/plain")), restored?.toList())
    }

    @Test
    fun プロバイダー読み取りのキャンセルを選択失敗に変えない(): Unit = runBlocking {
        assertFailsWith<CancellationException> {
            readPickedAttachmentMetadata(listOf("cancelled")) { throw CancellationException() }
        }
    }
}
