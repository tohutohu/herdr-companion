package com.tohutohu.herdrcompanion.ui.newsession

import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.data.DirectoryShortcuts
import kotlin.test.assertEquals
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

    @Test
    fun スクロールするほどお気に入りが多くても長押しで端まで並べ替えて落ちない() {
        val favorites = (1..10).map { "/w/fav$it" }
        var shortcuts by mutableStateOf(DirectoryShortcuts(favorites = favorites, recents = listOf("/w/recent")))
        rule.setContent {
            DirectoryShortcutsRow(
                shortcuts = shortcuts,
                currentPath = "/w/fav1",
                enabled = true,
                onOpen = {},
                onReorderFavorites = { shortcuts = shortcuts.copy(favorites = it) },
                modifier = Modifier.width(400.dp),
            )
        }

        // Hold the first chip at the row's right edge: the row scrolls the
        // chips it passes out of view while their reorder state changes.
        rule.onNodeWithText("fav1").performTouchInput { down(center) }
        rule.mainClock.advanceTimeBy(1_000)
        repeat(40) {
            rule.onNodeWithText("fav1").performTouchInput { moveBy(Offset(30f, 0f)) }
            rule.mainClock.advanceTimeBy(16)
        }
        rule.mainClock.advanceTimeBy(3_000)
        rule.onNodeWithText("fav1").performTouchInput { up() }
        rule.waitForIdle()

        assertEquals(favorites.drop(1) + "/w/fav1", shortcuts.favorites)
    }
}
