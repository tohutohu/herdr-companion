package com.tohutohu.herdrcompanion.ui.markdown

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performMouseInput
import androidx.compose.ui.unit.dp
import kotlin.random.Random
import org.junit.Rule
import org.junit.Test

@OptIn(ExperimentalTestApi::class, ExperimentalComposeUiApi::class)
class MarkdownSelectionTest {
    @get:Rule
    val rule = createComposeRule()

    private val markdown = """
        # 見出し

        最初の段落です。**太字**と`code`と[リンク](https://example.com)を含みます。
        二行目です。

        - 項目1
        - 項目2
          - 入れ子の項目
        1. 番号付き

        ```kotlin
        fun main() {
            println("hello")
        }
        ```

        > 引用です
        > 二行目

        | 列A | 列B |
        |---|---:|
        | 1 | 2 |
        | 3 | 4 |

        ---

        最後の段落です。
    """.trimIndent()

    @Test
    fun ドラッグで選択範囲を増減させても落ちない() {
        rule.setContent {
            SelectionContainer {
                Box(Modifier.testTag("md").width(360.dp)) { MarkdownText(markdown) }
            }
        }
        // Compose 1.12.0 drew a selection whose start and end were reversed
        // inside one Text and threw from the highlight; this seed hits it on
        // the 12th drag.
        val random = Random(1)
        repeat(60) {
            rule.onNodeWithTag("md").performMouseInput {
                fun point() = Offset(random.nextFloat() * width, random.nextFloat() * height)
                moveTo(point())
                press()
                repeat(12) { moveTo(point()) }
                release()
            }
            rule.waitForIdle()
        }
    }
}
