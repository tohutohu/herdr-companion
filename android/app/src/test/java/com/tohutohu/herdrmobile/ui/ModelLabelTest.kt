package com.tohutohu.herdrmobile.ui

import org.junit.Assert.assertEquals
import org.junit.Test

class ModelLabelTest {
    @Test
    fun `Claudeのモデル名はファミリーとバージョンの短い表記になる`() {
        assertEquals("Opus 5", modelLabel("claude-opus-5"))
        assertEquals("Fable 5.1", modelLabel("claude-fable-5-1"))
        assertEquals("Haiku 4.5", modelLabel("claude-haiku-4-5-20251001"))
        assertEquals("Opus 5 (1M)", modelLabel("claude-opus-5[1m]"))
    }

    @Test
    fun `それ以外のモデル名はそのまま表示する`() {
        assertEquals("gpt-6-astra", modelLabel("gpt-6-astra"))
        assertEquals("Sonnet 5", modelLabel("Sonnet 5"))
    }

    @Test
    fun `モデルとエフォートとモードを1行にまとめる`() {
        assertEquals("Opus 5 (high) · Plan", agentSettingsLabel("claude-opus-5", "high", "Plan"))
        assertEquals("gpt-6-astra (low)", agentSettingsLabel("gpt-6-astra", "low", null))
        assertEquals("effort max · Accept edits", agentSettingsLabel(null, "max", "Accept edits"))
        assertEquals("Default", agentSettingsLabel("", null, "Default"))
        assertEquals(null, agentSettingsLabel(null, "", " "))
    }
}
