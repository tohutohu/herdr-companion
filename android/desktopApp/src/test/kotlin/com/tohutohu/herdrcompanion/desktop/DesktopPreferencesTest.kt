package com.tohutohu.herdrcompanion.desktop

import com.tohutohu.herdrcompanion.data.AgentPreset
import com.tohutohu.herdrcompanion.data.AgentPresets
import java.awt.Rectangle
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class DesktopPreferencesTest {
    @Test
    fun `window snapshot round trips and clamps unsafe dimensions`() {
        val original = DesktopWindowSnapshot(
            width = 10_000,
            height = 100,
            x = 12,
            y = 34,
            maximized = true,
        )

        val restored = DesktopWindowSnapshot.decode(original.encode())

        assertEquals(4_096, restored?.width)
        assertEquals(560, restored?.height)
        assertEquals(12, restored?.x)
        assertEquals(34, restored?.y)
        assertTrue(restored?.maximized == true)
    }

    @Test
    fun `window position is accepted when it intersects a current display`() {
        val display = Rectangle(0, 0, 1_440, 900)

        assertTrue(
            windowIntersectsAnyScreen(
                DesktopWindowSnapshot(width = 900, height = 600, x = 1_300, y = 500),
                listOf(display),
            ),
        )
        assertFalse(
            windowIntersectsAnyScreen(
                DesktopWindowSnapshot(width = 900, height = 600, x = 2_000, y = 500),
                listOf(display),
            ),
        )
    }

    @Test
    fun `shortcut preferences preserve order and remove duplicate paths`() {
        val encoded = encodeLines(listOf("/work/a", "", "/work/b", "/work/a"))

        assertEquals("/work/a\n/work/b", encoded)
        assertEquals(listOf("/work/a", "/work/b"), decodeLines(encoded))
        assertEquals(emptyList(), decodeLines(null))
    }

    @Test
    fun `agent preset preferences round trip and invalid data starts empty`() {
        val value = AgentPresets(
            presets = listOf(AgentPreset("codex", "gpt-6-astra", "high", "Astra", "High")),
            lastUsed = AgentPreset("claude", model = "sonnet"),
            lastWorktree = true,
            lastTranslation = true,
        )

        assertEquals(value, decodeAgentPresets(encodeAgentPresets(value)))
        assertEquals(AgentPresets(), decodeAgentPresets("not json"))
    }

    @Test
    fun `以前の設定に翻訳モードがなければオフで復元する`() {
        val loaded = decodeAgentPresets("""{"lastUsed":{"provider":"codex"},"lastWorktree":true}""")

        assertEquals("codex", loaded.lastUsed?.provider)
        assertTrue(loaded.lastWorktree)
        assertFalse(loaded.lastTranslation)
    }

    @Test
    fun `翻訳モードをオフに戻した設定も保存できる`() {
        val enabled = AgentPresets(lastTranslation = true)
        val disabled = enabled.copy(lastTranslation = false)

        assertFalse(decodeAgentPresets(encodeAgentPresets(disabled)).lastTranslation)
    }

    @Test
    fun `廃止したエージェントのプリセットは読み込み時に除く`() {
        val saved = AgentPresets(
            presets = listOf(AgentPreset("opencode"), AgentPreset("codex", "gpt-6-astra"), AgentPreset("devin")),
            lastUsed = AgentPreset("opencode"),
        )

        val loaded = decodeAgentPresets(encodeAgentPresets(saved))
        assertEquals(listOf("codex"), loaded.presets.map { it.provider })
        assertEquals(null, loaded.lastUsed)
    }
}
