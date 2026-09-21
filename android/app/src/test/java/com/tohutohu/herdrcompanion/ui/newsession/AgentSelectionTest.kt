package com.tohutohu.herdrcompanion.ui.newsession

import com.tohutohu.herdrcompanion.data.AgentPreset
import com.tohutohu.herdrcompanion.data.api.EffortOptionDto
import com.tohutohu.herdrcompanion.data.api.ModeOptionDto
import com.tohutohu.herdrcompanion.data.api.ModelOptionDto
import com.tohutohu.herdrcompanion.data.api.ModelsResponse
import org.junit.Assert.assertEquals
import org.junit.Test

class AgentSelectionTest {
    private val catalog = ModelsResponse(
        models = listOf(
            ModelOptionDto(
                id = "gpt-6-astra",
                name = "GPT-6 Astra",
                efforts = listOf(EffortOptionDto("low", "Low"), EffortOptionDto("xhigh", "Extra high")),
            ),
        ),
        efforts = listOf(EffortOptionDto("medium", "Medium")),
        modes = listOf(ModeOptionDto("default", "Default", default = true), ModeOptionDto("plan", "Plan")),
    )

    @Test
    fun `選んだ組み合わせにカタログの表示名を付ける`() {
        assertEquals(
            AgentPreset("codex", "gpt-6-astra", "xhigh", "plan", "GPT-6 Astra", "Extra high", "Plan"),
            agentPreset("codex", "gpt-6-astra", "xhigh", "plan", catalog),
        )
    }

    @Test
    fun `既定のままならモデル名もエフォート名もモード名も空になる`() {
        assertEquals(AgentPreset("claude"), agentPreset("claude", "", "", "", ModelsResponse()))
    }

    @Test
    fun `カタログに無いモデルは表示名が付かない`() {
        assertEquals(
            AgentPreset("codex", "gpt-6-retired", "low"),
            agentPreset("codex", "gpt-6-retired", "low", "", catalog),
        )
    }

    @Test
    fun `エージェント自身のモードは選んでも空として扱う`() {
        assertEquals("", normalizedMode(catalog, "default"))
        assertEquals("plan", normalizedMode(catalog, "plan"))
        assertEquals("", normalizedMode(ModelsResponse(), ""))
    }

    @Test
    fun `ラベルはエージェントとモデルとエフォートとモードを並べる`() {
        assertEquals(
            "Codex GPT-6 Astra · Extra high · Plan",
            presetLabel(
                AgentPreset("codex", "gpt-6-astra", "xhigh", "plan", "GPT-6 Astra", "Extra high", "Plan"),
            ),
        )
        assertEquals("Claude Opus", presetLabel(AgentPreset("claude", "opus", modelName = "Opus")))
        assertEquals("Claude default · High", presetLabel(AgentPreset("claude", "", "high", effortName = "High")))
        assertEquals("Claude default", presetLabel(AgentPreset("claude")))
    }

    @Test
    fun `既定のモードはラベルに出さない`() {
        val preset = agentPreset("codex", "", "", normalizedMode(catalog, "default"), catalog)
        assertEquals("Codex default", presetLabel(preset))
    }

    @Test
    fun `表示名が無いときはidをラベルにする`() {
        assertEquals("Codex gpt-6-astra · xhigh · plan", presetLabel(AgentPreset("codex", "gpt-6-astra", "xhigh", "plan")))
    }

    @Test
    fun `カタログ読み込み前は保存済みの組み合わせから表示名を補う`() {
        val known = listOf(AgentPreset("codex", "gpt-6-astra", "xhigh", "plan", "GPT-6 Astra", "Extra high", "Plan"))
        assertEquals(
            known.first(),
            withKnownNames(AgentPreset("codex", "gpt-6-astra", "xhigh", "plan"), known),
        )
    }

    @Test
    fun `保存済みに無い組み合わせはそのまま返す`() {
        val current = AgentPreset("claude", "opus", "high")
        assertEquals(current, withKnownNames(current, listOf(AgentPreset("claude", "sonnet", modelName = "Sonnet"))))
    }
}
