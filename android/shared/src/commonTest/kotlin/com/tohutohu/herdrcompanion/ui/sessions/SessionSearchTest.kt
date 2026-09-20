package com.tohutohu.herdrcompanion.ui.sessions

import com.tohutohu.herdrcompanion.model.SessionUiModel
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class SessionSearchTest {
    @Test
    fun `session search matches useful fields case insensitively`() {
        val rows = listOf(
            row(id = "claude-1", project = "herdr-mobile", model = "opus-4", provider = "claude"),
            row(id = "codex-2", project = "billing-tools", model = "gpt-5-codex", provider = "codex"),
        )

        assertEquals(listOf("claude-1"), rows.filterSessionSearch(" HERDR ").map { it.session.id })
        assertEquals(listOf("codex-2"), rows.filterSessionSearch("gpt-5").map { it.session.id })
        assertEquals(listOf("claude-1"), rows.filterSessionSearch("CLAUDE").map { it.session.id })
        assertTrue(rows.filterSessionSearch("missing").isEmpty())
        assertEquals(2, rows.filterSessionSearch("").size)
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
