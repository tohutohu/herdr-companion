package com.tohutohu.herdrcompanion.ui.sessions

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.longClick
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTouchInput
import com.tohutohu.herdrcompanion.model.SessionUiModel
import org.junit.Rule
import org.junit.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse

class SessionColorTest {
    @get:Rule val rule = createComposeRule()

    @Test
    fun 長押しから複数セッションを色分けして解除できる() {
        val selection = SessionSelection()
        var rows by mutableStateOf(listOf(row("one"), row("two")))
        val changes = mutableListOf<SessionListAction.SetColor>()
        rule.setContent {
            MaterialTheme {
                SessionListScreen(
                    state = SessionListUiState(sessions = rows, isLoaded = true),
                    selection = selection,
                    onAction = { action ->
                        if (action is SessionListAction.SetColor) {
                            changes += action
                            val ids = action.sessions.map { it.id }
                            rows = rows.map { if (it.session.id in ids) it.copy(color = action.color) else it }
                        }
                    },
                )
            }
        }
        rule.onNodeWithText("one").performTouchInput { longClick() }
        rule.onNodeWithText("two").performClick()
        rule.onNodeWithContentDescription("Set session color").performClick()
        rule.onNodeWithText("Blue").performClick()
        rule.runOnIdle {
            assertEquals(listOf("one", "two"), changes.single().sessions.map { it.id })
            assertEquals(SessionColor.Blue, changes.single().color)
            assertFalse(selection.active)
            assertEquals(listOf(SessionColor.Blue, SessionColor.Blue), rows.map { it.color })
        }
        rule.onNodeWithText("one").performTouchInput { longClick() }
        rule.onNodeWithContentDescription("Set session color").performClick()
        rule.onNodeWithText("No color").performClick()
        rule.runOnIdle {
            assertEquals(listOf(null, SessionColor.Blue), rows.map { it.color })
        }
        rule.onNodeWithContentDescription("Session color: Blue", useUnmergedTree = true).assertExists()
    }

    @Test
    fun 色選択をキャンセルしても選択と色を保つ() {
        val selection = SessionSelection().apply { toggle("one") }
        rule.setContent {
            MaterialTheme {
                SelectionTopBar(
                    selection = selection,
                    all = listOf(SessionRef("one", false, false)),
                    onArchive = {}, onUnarchive = {}, onResume = {},
                    onSetColor = { _, _ -> error("キャンセルでは保存しない") },
                    colors = mapOf("one" to SessionColor.Green),
                )
            }
        }
        rule.onNodeWithContentDescription("Set session color").performClick()
        rule.onNodeWithContentDescription("Current color").assertExists()
        rule.onNodeWithText("Cancel").performClick()
        rule.runOnIdle { assertEquals(setOf("one"), selection.ids) }
    }

    private fun row(id: String) = SessionListItemUiState(
        session = SessionUiModel(
            id = id, provider = "codex", providerName = "Codex", project = id,
            title = null, cwd = null, status = "idle", lastMessage = null, canSend = true,
            model = null, effort = null, mode = null, contextUsedTokens = null,
            contextWindowTokens = null, contextUsedPercent = null, costUsd = null,
            costEstimated = null, archived = false, live = true,
        ),
        relativeUpdatedAt = "now",
    )
}
