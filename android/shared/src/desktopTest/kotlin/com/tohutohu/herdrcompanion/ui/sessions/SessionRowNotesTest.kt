package com.tohutohu.herdrcompanion.ui.sessions

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import com.tohutohu.herdrcompanion.model.SessionUiModel
import org.junit.Rule
import org.junit.Test

class SessionRowNotesTest {
    @get:Rule val rule = createComposeRule()

    @Test
    fun 一覧の行にセッションのメモを表示する() {
        show(row("one", notes = listOf("PRのレビュー待ち")), row("two"))

        rule.onNodeWithText("PRのレビュー待ち").assertExists()
        rule.onNodeWithTag("sessionRowNotes", useUnmergedTree = true).assertExists()
    }

    @Test
    fun メモが多いときは新しいものだけ表示して残りの件数を出す() {
        show(row("one", notes = listOf("一番古いメモ", "二番目のメモ", "最新のメモ")))

        rule.onNodeWithText("一番古いメモ").assertDoesNotExist()
        rule.onNodeWithText("二番目のメモ").assertExists()
        rule.onNodeWithText("最新のメモ").assertExists()
        rule.onNodeWithText("+1 more note").assertExists()
    }

    @Test
    fun メモが無いセッションにはメモ欄を出さない() {
        show(row("one"))

        rule.onNodeWithTag("sessionRowNotes", useUnmergedTree = true).assertDoesNotExist()
    }

    private fun show(vararg rows: SessionListItemUiState) {
        rule.setContent {
            MaterialTheme {
                SessionListScreen(
                    state = SessionListUiState(sessions = rows.toList(), isLoaded = true),
                    onAction = {},
                )
            }
        }
    }

    private fun row(id: String, notes: List<String> = emptyList()) = SessionListItemUiState(
        session = SessionUiModel(
            id = id, provider = "codex", providerName = "Codex", project = id,
            title = null, cwd = null, status = "idle", lastMessage = null, canSend = true,
            model = null, effort = null, mode = null, contextUsedTokens = null,
            contextWindowTokens = null, contextUsedPercent = null, costUsd = null,
            costEstimated = null, archived = false, live = true, notes = notes,
        ),
        relativeUpdatedAt = "now",
    )
}
