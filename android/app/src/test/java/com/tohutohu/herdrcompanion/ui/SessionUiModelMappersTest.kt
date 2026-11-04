package com.tohutohu.herdrcompanion.ui

import com.tohutohu.herdrcompanion.data.db.SessionEntity
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class SessionUiModelMappersTest {
    @Test
    fun `未読状態を一覧モデルへ引き継ぐ`() {
        val session = SessionEntity(
            id = "claude:session",
            provider = "claude",
            providerName = "Claude Code",
            project = "project",
            title = null,
            cwd = "/workspace/project",
            status = "completed",
            updatedAt = 1L,
            lastMessage = "done",
            canSend = true,
            listed = true,
            lastSyncedAt = 1L,
            unread = true,
        )

        assertTrue(session.toUiModel().unread)
    }
    @Test
    fun `キャッシュした裏の作業を一覧モデルへ引き継ぎ壊れていれば無視する`() {
        val session = SessionEntity(
            id = "claude:session",
            provider = "claude",
            providerName = "Claude Code",
            project = "project",
            title = null,
            cwd = null,
            status = "completed",
            updatedAt = 1L,
            lastMessage = null,
            canSend = true,
            listed = true,
            lastSyncedAt = 1L,
            backgroundJson = """[{"id":"t1","kind":"agent","description":"review","startedAt":"2026-11-04T12:00:00Z"}]""",
        )

        assertEquals(listOf("review"), session.toUiModel().background.map { it.description })
        assertTrue(session.copy(backgroundJson = "{broken").toUiModel().background.isEmpty())
        assertTrue(session.copy(backgroundJson = null).toUiModel().background.isEmpty())
    }

    @Test
    fun `キャッシュした保留中の設定と失敗理由を一覧モデルへ引き継ぐ`() {
        val session = SessionEntity(
            id = "claude:session",
            provider = "claude",
            providerName = "Claude Code",
            project = "project",
            title = null,
            cwd = "/workspace/project",
            status = "running",
            updatedAt = 1L,
            lastMessage = null,
            canSend = true,
            listed = true,
            lastSyncedAt = 1L,
            pendingModel = "sonnet",
            pendingEffort = "high",
            settingsApply = "next_request",
            error = "boom",
        )

        val model = session.toUiModel()
        assertTrue(model.live)
        assertEquals("sonnet", model.pendingModel)
        assertEquals("high", model.pendingEffort)
        assertEquals("next_request", model.settingsApply)
        assertEquals("boom", model.error)
    }
}
