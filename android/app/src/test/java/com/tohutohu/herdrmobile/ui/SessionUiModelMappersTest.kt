package com.tohutohu.herdrmobile.ui

import com.tohutohu.herdrmobile.data.db.SessionEntity
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
}
