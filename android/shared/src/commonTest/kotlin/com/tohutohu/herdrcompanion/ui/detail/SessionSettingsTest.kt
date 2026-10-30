package com.tohutohu.herdrcompanion.ui.detail

import com.tohutohu.herdrcompanion.data.api.ModeOptionDto
import com.tohutohu.herdrcompanion.data.api.ModelsResponse
import com.tohutohu.herdrcompanion.data.api.SessionDto
import com.tohutohu.herdrcompanion.model.toUiModel
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class SessionSettingsTest {
    private fun session(provider: String, status: String = "running") =
        SessionDto(id = "$provider:1", provider = provider, status = status, updatedAt = "").toUiModel()

    @Test
    fun `稼働中のClaudeとCodexだけがモデルとエフォートを変更できる`() {
        assertTrue(settingsChangeable(session("claude")))
        assertTrue(settingsChangeable(session("codex")))
        assertFalse(settingsChangeable(session("unknown")))
        assertFalse(settingsChangeable(session("codex", status = "offline")))
    }

    @Test
    fun `保留中の変更はいつ適用されるかを添えて表示する`() {
        assertEquals("→ Sonnet, applies from the next model request", pendingSettingLabel("Sonnet", "next_request"))
        assertEquals("→ GPT-6 Mini, applies from the next turn", pendingSettingLabel("GPT-6 Mini", "next_turn"))
        assertEquals("→ High, not applied yet", pendingSettingLabel("High", null))
    }

    @Test
    fun `表示名で報告されたモードをカタログのIDに対応させる`() {
        val catalog = ModelsResponse(
            modes = listOf(ModeOptionDto("default", "Default", default = true), ModeOptionDto("plan", "Plan mode")),
        )
        assertEquals("plan", currentModeId(catalog, "plan mode"))
        assertEquals("plan", currentModeId(catalog, "plan"))
        assertEquals("default", currentModeId(catalog, null))
        // A mode the catalog does not know keeps its label.
        assertEquals("bypass", currentModeId(catalog, "bypass"))
    }
}
