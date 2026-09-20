package com.tohutohu.herdrcompanion.ui.newsession

import com.tohutohu.herdrcompanion.data.AgentPreset
import com.tohutohu.herdrcompanion.data.api.EffortOptionDto
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
    )

    @Test
    fun `選んだ組み合わせにカタログの表示名を付ける`() {
        assertEquals(
            AgentPreset("codex", "gpt-6-astra", "xhigh", "GPT-6 Astra", "Extra high"),
            agentPreset("codex", "gpt-6-astra", "xhigh", catalog),
        )
    }

    @Test
    fun `既定のままならモデル名もエフォート名も空になる`() {
        assertEquals(AgentPreset("claude"), agentPreset("claude", "", "", ModelsResponse()))
    }

    @Test
    fun `カタログに無いモデルは表示名が付かない`() {
        assertEquals(
            AgentPreset("codex", "gpt-6-retired", "low"),
            agentPreset("codex", "gpt-6-retired", "low", catalog),
        )
    }

    @Test
    fun `ラベルはエージェントとモデルとエフォートを並べる`() {
        assertEquals(
            "Codex GPT-6 Astra · Extra high",
            presetLabel(AgentPreset("codex", "gpt-6-astra", "xhigh", "GPT-6 Astra", "Extra high")),
        )
        assertEquals("Claude Opus", presetLabel(AgentPreset("claude", "opus", "", "Opus")))
        assertEquals("Claude default · High", presetLabel(AgentPreset("claude", "", "high", effortName = "High")))
        assertEquals("Claude default", presetLabel(AgentPreset("claude")))
    }

    @Test
    fun `表示名が無いときはidをラベルにする`() {
        assertEquals("Codex gpt-6-astra · xhigh", presetLabel(AgentPreset("codex", "gpt-6-astra", "xhigh")))
    }

    @Test
    fun `カタログ読み込み前は保存済みの組み合わせから表示名を補う`() {
        val known = listOf(AgentPreset("codex", "gpt-6-astra", "xhigh", "GPT-6 Astra", "Extra high"))
        assertEquals(
            known.first(),
            withKnownNames(AgentPreset("codex", "gpt-6-astra", "xhigh"), known),
        )
    }

    @Test
    fun `保存済みに無い組み合わせはそのまま返す`() {
        val current = AgentPreset("claude", "opus", "high")
        assertEquals(current, withKnownNames(current, listOf(AgentPreset("claude", "sonnet", "", "Sonnet"))))
    }
}
