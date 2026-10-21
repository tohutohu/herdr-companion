package com.tohutohu.herdrcompanion.ui.newsession

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithText
import com.tohutohu.herdrcompanion.data.DirectoryShortcuts
import org.junit.Rule
import org.junit.Test

class DirectoryShortcutsRowTest {
    @get:Rule
    val rule = createComposeRule()

    @Test
    fun 最近使っていないフォルダのお気に入りを外しても落ちない() {
        var shortcuts by mutableStateOf(DirectoryShortcuts(favorites = listOf("/w/a", "/w/b")))
        rule.setContent {
            DirectoryShortcutsRow(
                shortcuts = shortcuts,
                currentPath = "/w/b",
                enabled = true,
                onOpen = {},
                onReorderFavorites = {},
            )
        }
        rule.onNodeWithText("b").assertExists()

        shortcuts = shortcuts.copy(favorites = listOf("/w/a"))
        rule.waitForIdle()

        rule.onNodeWithText("a").assertExists()
        rule.onAllNodesWithText("b").assertCountEquals(0)
    }
}
