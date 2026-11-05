package com.tohutohu.herdrcompanion.ui.newsession

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertIsOff
import androidx.compose.ui.test.assertIsOn
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.performClick
import org.junit.Rule
import org.junit.Test

class TranslationModeButtonTest {
    @get:Rule val rule = createComposeRule()

    @Test
    fun 翻訳アイコンでオンとオフを切り替え開始処理中は変更しない() {
        var checked by mutableStateOf(false)
        var enabled by mutableStateOf(true)
        rule.setContent {
            MaterialTheme {
                TranslationModeButton(checked, enabled) { checked = it }
            }
        }
        rule.onNodeWithContentDescription("Translation mode").assertIsOff().performClick().assertIsOn()
        rule.onNodeWithContentDescription("Translation mode").performClick().assertIsOff()
        rule.runOnIdle { checked = true; enabled = false }
        rule.onNodeWithContentDescription("Translation mode").assertIsOn().assertIsNotEnabled()
    }
}
