package com.tohutohu.herdrcompanion.data

import org.junit.Assert.assertEquals
import org.junit.Test

class AgentPresetsTest {
    private val opus = AgentPreset("claude", "opus", "high", "Opus", "High")
    private val astra = AgentPreset("codex", "gpt-6-astra", "low", "GPT-6 Astra", "Low")

    @Test
    fun `お気に入りは末尾に足される`() {
        assertEquals(listOf(opus, astra), togglePreset(listOf(opus), astra))
    }

    @Test
    fun `同じ組み合わせをもう一度登録すると外れる`() {
        assertEquals(listOf(astra), togglePreset(listOf(opus, astra), opus))
    }

    @Test
    fun `表示名が違っても同じ組み合わせとして外れる`() {
        assertEquals(emptyList<AgentPreset>(), togglePreset(listOf(opus), opus.copy(modelName = "Opus 5")))
    }

    @Test
    fun `モデルやエフォートが違えば別の組み合わせになる`() {
        val low = opus.copy(effort = "low", effortName = "Low")
        assertEquals(listOf(opus, low), togglePreset(listOf(opus), low))
    }

    @Test
    fun `お気に入りモデルは指定位置へ移動できる`() {
        assertEquals(listOf(astra, opus), movePreset(listOf(opus, astra), 0, 1))
    }
}
