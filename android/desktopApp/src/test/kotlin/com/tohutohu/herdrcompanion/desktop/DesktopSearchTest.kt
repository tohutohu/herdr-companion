package com.tohutohu.herdrcompanion.desktop

import com.tohutohu.herdrcompanion.model.SessionUiModel
import com.tohutohu.herdrcompanion.ui.sessions.SessionListItemUiState
import kotlin.test.Test
import kotlin.test.assertEquals

class DesktopSearchTest {
    @Test
    fun `search matches useful local session fields`() {
        val rows = listOf(
            row(
                id = "claude-1",
                project = "herdr-mobile",
                model = "opus-4",
                provider = "claude",
            ),
            row(
                id = "codex-2",
                project = "billing-tools",
                model = "gpt-5-codex",
                provider = "codex",
            ),
        )

        assertEquals(listOf("claude-1"), rows.filterDesktopSessions("HERDR").map { it.session.id })
        assertEquals(listOf("codex-2"), rows.filterDesktopSessions("gpt-5").map { it.session.id })
        assertEquals(listOf("claude-1"), rows.filterDesktopSessions("claude").map { it.session.id })
        assertEquals(0, rows.filterDesktopSessions("missing").size)
    }

    @Test
    fun `search selection stays within filtered candidates`() {
        assertEquals(0, nextDesktopSearchIndex(0, 0, 1))
        assertEquals(0, nextDesktopSearchIndex(0, 3, -1))
        assertEquals(2, nextDesktopSearchIndex(1, 3, 10))
    }

    private fun row(
        id: String,
        project: String,
        model: String,
        provider: String,
    ) = SessionListItemUiState(
        session = SessionUiModel(
            id = id,
            provider = provider,
            providerName = provider,
            project = project,
            title = null,
            cwd = "/Users/test/$project",
            status = "running",
            lastMessage = "working",
            paneId = null,
            canSend = true,
            model = model,
            effort = null,
            mode = null,
            contextUsedTokens = null,
            contextWindowTokens = null,
            contextUsedPercent = null,
            costUsd = null,
            costEstimated = null,
            archived = false,
            live = true,
        ),
        relativeUpdatedAt = "just now",
    )
}
