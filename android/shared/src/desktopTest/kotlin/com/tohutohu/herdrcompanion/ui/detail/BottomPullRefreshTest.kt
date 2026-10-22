package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.swipeDown
import androidx.compose.ui.test.swipeUp
import com.tohutohu.herdrcompanion.data.Message
import com.tohutohu.herdrcompanion.data.api.BlockDto
import kotlin.test.assertEquals
import org.junit.Rule
import org.junit.Test

class BottomPullRefreshTest {
    @get:Rule val rule = createComposeRule()

    private val actions = mutableListOf<SessionDetailAction>()

    private fun show(pullToRefresh: Boolean = true, pullRefreshing: Boolean = false) {
        val messages = listOf(
            Message("m1", "user", 1_000, listOf(BlockDto("text", text = "テストを実行して"))),
            Message("m2", "assistant", 2_000, listOf(BlockDto("text", text = "最新の報告"))),
        )
        rule.setContent {
            MaterialTheme {
                SessionDetailScreen(
                    state = SessionDetailUiState(
                        "claude:current", null, messages, emptyList(), error = null, answering = false,
                        pullToRefresh = pullToRefresh, pullRefreshing = pullRefreshing,
                    ),
                    resolveUrl = { it }, formatFileSize = { "$it bytes" }, formatTime = { "$it" },
                    onAction = { actions += it },
                )
            }
        }
    }

    private fun pullUp(distance: Float) {
        rule.onNodeWithText("最新の報告").performTouchInput {
            swipeUp(startY = bottom, endY = bottom - distance, durationMillis = 400)
        }
        rule.waitForIdle()
    }

    private fun pulls() = actions.count { it == SessionDetailAction.PullRefresh }

    @Test
    fun 最新メッセージの先まで十分に引き上げると取得を求める() {
        show()
        pullUp(400f)
        assertEquals(1, pulls())
    }

    @Test
    fun 引き上げが足りなければ取得しない() {
        show()
        pullUp(80f)
        assertEquals(0, pulls())
        rule.onNodeWithTag(BOTTOM_PULL_INDICATOR_TAG).assertDoesNotExist()
    }

    @Test
    fun 古いメッセージへ戻るスクロールでは取得しない() {
        show()
        rule.onNodeWithText("最新の報告").performTouchInput {
            swipeDown(startY = top, endY = top + 400f, durationMillis = 400)
        }
        rule.waitForIdle()
        assertEquals(0, pulls())
    }

    @Test
    fun 引き上げを使わない画面では取得しない() {
        show(pullToRefresh = false)
        pullUp(400f)
        assertEquals(0, pulls())
    }

    @Test
    fun 取得中はインジケーターを表示し続ける() {
        show(pullRefreshing = true)
        rule.onNodeWithTag(BOTTOM_PULL_INDICATOR_TAG).assertExists()
    }
}
