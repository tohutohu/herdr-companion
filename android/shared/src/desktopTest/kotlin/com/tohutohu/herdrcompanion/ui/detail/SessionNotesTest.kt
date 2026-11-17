package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextClearance
import androidx.compose.ui.test.performTextInput
import com.tohutohu.herdrcompanion.data.api.NoteDto
import com.tohutohu.herdrcompanion.data.api.SessionDto
import com.tohutohu.herdrcompanion.model.toUiModel
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue
import kotlin.time.Instant
import org.junit.Rule
import org.junit.Test

class SessionNotesTest {
    @get:Rule val rule = createComposeRule()

    private class FakeBackend : SessionNotesBackend {
        val notes = mutableListOf<NoteDto>()
        val added = mutableListOf<Triple<String, String, String?>>()
        var unsupported = false
        var failSave: String? = null
        private var next = 0

        override suspend fun list(sessionId: String): List<NoteDto> {
            if (unsupported) throw NotesUnsupportedException()
            return notes.filter { it.sessionId == sessionId }
        }

        override suspend fun add(sessionId: String, text: String, expiresAt: String?): NoteDto {
            failSave?.let { throw IllegalStateException(it) }
            added += Triple(sessionId, text, expiresAt)
            return NoteDto("n${next++}", sessionId, text, expiresAt = expiresAt).also { notes += it }
        }

        override suspend fun update(noteId: String, text: String): NoteDto {
            val i = notes.indexOfFirst { it.id == noteId }
            return notes[i].copy(text = text).also { notes[i] = it }
        }

        override suspend fun delete(noteId: String) {
            notes.removeAll { it.id == noteId }
        }
    }

    private val backend = FakeBackend()
    private val now = Instant.parse("2026-11-17T03:00:00Z")
    private val notes = SessionNotes(CoroutineScope(Dispatchers.Unconfined), backend) { now }

    private fun state(id: String, status: String = "running") = SessionDetailUiState(
        id,
        SessionDto(id = id, provider = "claude", status = status, canSend = status != "offline", updatedAt = "").toUiModel(),
        emptyList(), emptyList(), error = null, answering = false,
    )

    @Test
    fun 期限を選んで書いたメモは期限付きで追加され入力欄が空になる() {
        notes.setOpen("claude:a", true)
        notes.setDraft("claude:a", "  PRがマージされたらアーカイブしてよい ")
        notes.setExpiry("claude:a", NoteExpiry.DAY)
        notes.save("claude:a")

        assertEquals(Triple("claude:a", "PRがマージされたらアーカイブしてよい", "2026-11-18T03:00:00Z"), backend.added.single())
        val s = notes["claude:a"]
        assertEquals(listOf("PRがマージされたらアーカイブしてよい"), s.notes?.map { it.text })
        assertEquals("", s.draft)
        assertEquals(NoteExpiry.NONE, s.expiry)
        assertEquals(false, s.open)
    }

    @Test
    fun 書き換えは同じメモを置き換え閉じると編集をやめても新しいメモの下書きは残る() {
        backend.notes += NoteDto("n9", "claude:a", "待機させておく")
        notes.setOpen("claude:a", true)
        notes.startEdit("claude:a", "n9")
        assertEquals("待機させておく", notes["claude:a"].draft)
        notes.setDraft("claude:a", "レビューが来たら対応させる")
        notes.save("claude:a")
        assertEquals(listOf("レビューが来たら対応させる"), notes["claude:a"].notes?.map { it.text })
        assertNull(notes["claude:a"].editing)

        notes.startEdit("claude:a", "n9")
        notes.setOpen("claude:a", false)
        assertEquals("", notes["claude:a"].draft)
        notes.setDraft("claude:a", "書きかけ")
        notes.setOpen("claude:a", false)
        assertEquals("書きかけ", notes["claude:a"].draft)
    }

    @Test
    fun 保存に失敗したら下書きを残して理由を出す() {
        backend.failSave = "this session already has 50 notes"
        notes.setDraft("claude:a", "メモ")
        notes.save("claude:a")
        assertEquals("this session already has 50 notes", notes["claude:a"].error)
        assertEquals("メモ", notes["claude:a"].draft)
        assertTrue(notes["claude:a"].canSave)
    }

    @Test
    fun 編集中のメモを消すと編集もやめる() {
        backend.notes += NoteDto("n1", "claude:a", "一つ目")
        notes.refresh("claude:a")
        notes.startEdit("claude:a", "n1")
        notes.delete("claude:a", "n1")
        assertEquals(emptyList(), notes["claude:a"].notes)
        assertNull(notes["claude:a"].editing)
        assertEquals("", notes["claude:a"].draft)
    }

    @Test
    fun メモの補足には書いた時刻と書いたエージェントと期限を出す() {
        val note = NoteDto(
            "n1", "claude:a", "t",
            author = "claude:5b27a61d-0000-4000-8000-000000000000",
            createdAt = "2026-11-17T01:00:00Z", updatedAt = "2026-11-17T01:00:00Z",
            expiresAt = "2026-11-19T03:00:00Z",
        )
        assertEquals("2h ago · by claude:5b27a61d · expires in 2d", noteDetails(note, now))
        assertEquals("just now", noteDetails(NoteDto("n2", text = "t", updatedAt = "2026-11-17T02:59:40Z"), now))
    }

    private fun show(status: String = "running") {
        rule.setContent {
            MaterialTheme {
                SessionDetailScreen(
                    state = state("claude:a", status),
                    resolveUrl = { it }, formatFileSize = { "$it bytes" }, formatTime = { "$it" },
                    notes = notes,
                    onAction = {},
                )
            }
        }
    }

    @Test
    fun メモは開かなくても入力欄の上に出ている() {
        backend.notes += NoteDto("n1", "claude:a", "CIが落ちたら直させて")
        show()
        rule.onNodeWithTag("sessionNotes").assertExists()
        rule.onNodeWithText("CIが落ちたら直させて").assertExists()
        rule.onNodeWithTag("noteDraft").assertDoesNotExist()
    }

    @Test
    fun 追加メニューから入力欄の上にメモを書くと確定して欄が閉じる() {
        backend.notes += NoteDto("n1", "claude:a", "CIが落ちたら直させて")
        show()
        rule.onNodeWithContentDescription("Attach").performClick()
        rule.onNodeWithText("Add note").performClick()
        rule.onNodeWithTag("noteDraft").performTextInput("マージされたらアーカイブしてよい")
        rule.onNodeWithText("Add note").performClick()
        rule.onNodeWithText("マージされたらアーカイブしてよい").assertExists()
        rule.onNodeWithText("CIが落ちたら直させて").assertExists()
        rule.onNodeWithTag("noteDraft").assertDoesNotExist()
        assertEquals("マージされたらアーカイブしてよい", backend.added.single().second)
    }

    @Test
    fun メモを押すと書き換えと削除ができる() {
        backend.notes += NoteDto("n1", "claude:a", "待機させておく", updatedAt = "2026-11-17T01:00:00Z")
        show()
        rule.onNodeWithText("待機させておく").performClick()
        rule.onNodeWithText("Edit").performClick()
        rule.onNodeWithTag("noteDraft").performTextClearance()
        rule.onNodeWithTag("noteDraft").performTextInput("レビューが来たら対応させる")
        rule.onNodeWithText("Save").performClick()
        rule.onNodeWithText("レビューが来たら対応させる").assertExists()
        assertEquals("レビューが来たら対応させる", backend.notes.single().text)

        rule.onNodeWithText("レビューが来たら対応させる").performClick()
        rule.onNodeWithText("Remove").performClick()
        rule.onNodeWithTag("sessionNotes").assertDoesNotExist()
        assertTrue(backend.notes.isEmpty())
    }

    @Test
    fun 停止中のセッションでもメモが見えてメニューから書ける() {
        backend.notes += NoteDto("n1", "claude:a", "再開は不要")
        show(status = "offline")
        rule.onNodeWithText("再開は不要").assertExists()
        rule.onNodeWithContentDescription("Session actions").performClick()
        rule.onNodeWithText("Add note").performClick()
        rule.onNodeWithTag("noteDraft").assertExists()
    }

    @Test
    fun メモに対応しないGatewayでは更新を促し入力欄を出さない() {
        backend.unsupported = true
        show()
        rule.onNodeWithContentDescription("Attach").performClick()
        rule.onNodeWithText("Add note").performClick()
        rule.onNodeWithText("This Gateway keeps no notes. Update the Gateway.").assertExists()
        rule.onNodeWithTag("noteDraft").assertDoesNotExist()
    }
}
