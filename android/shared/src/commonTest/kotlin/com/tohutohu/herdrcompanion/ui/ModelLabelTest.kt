package com.tohutohu.herdrcompanion.ui

import kotlin.test.Test
import kotlin.test.assertEquals

class ModelLabelTest {
    @Test
    fun `セッションのモードをモデル情報と一緒に表示する`() {
        assertEquals("Opus 5 (high) · Plan", agentSettingsLabel("claude-opus-5", "high", "Plan"))
    }
}
