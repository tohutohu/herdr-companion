package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.tohutohu.herdrcompanion.model.BackgroundTaskUiModel
import org.junit.Rule
import org.junit.Test

class BackgroundTasksBarTest {
    @get:Rule val rule = createComposeRule()

    private val tasks = listOf(
        BackgroundTaskUiModel("t1", "agent", "Review the diff", "2026-11-04T12:00:00Z"),
        BackgroundTaskUiModel("t2", "shell", null, ""),
    )

    @Test
    fun 件数だけ見せて開くと内容を一覧する() {
        rule.setContent { MaterialTheme { BackgroundTasksBar("claude:1", tasks) } }
        rule.onNodeWithText("2 in background", substring = true).assertExists()
        rule.onNodeWithText("Review the diff").assertDoesNotExist()

        rule.onNodeWithContentDescription("Show background tasks").performClick()

        rule.onNodeWithText("Agent").assertExists()
        rule.onNodeWithText("Review the diff").assertExists()
        // A task without a description is still told apart by its id.
        rule.onNodeWithText("Shell").assertExists()
        rule.onNodeWithText("t2").assertExists()
    }
}
