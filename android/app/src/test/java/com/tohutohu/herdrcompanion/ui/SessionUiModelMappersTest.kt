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
            paneId = "pane",
            canSend = true,
            listed = true,
            lastSyncedAt = 1L,
            unread = true,
        )

        assertTrue(session.toUiModel().unread)
    }
    @Test
    fun `キャッシュした直接実行の実行先と保留中の設定を一覧モデルへ引き継ぐ`() {
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
            paneId = null,
            canSend = true,
            listed = true,
            lastSyncedAt = 1L,
            backend = "direct",
            pendingModel = "sonnet",
            pendingEffort = "high",
            settingsApply = "next_request",
            error = "boom",
        )

        val model = session.toUiModel()
        assertTrue(model.direct)
        assertEquals("sonnet", model.pendingModel)
        assertEquals("high", model.pendingEffort)
        assertEquals("next_request", model.settingsApply)
        assertEquals("boom", model.error)
    }
}
