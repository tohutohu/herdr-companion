package com.tohutohu.herdrmobile.ui.usage

import com.tohutohu.herdrmobile.data.api.UsageProviderDto
import com.tohutohu.herdrmobile.data.api.UsageWindowDto
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class UsageFormatTest {
    private fun window(label: String, used: Int, scope: String? = null) =
        UsageWindowDto(key = label, label = label, scope = scope, usedPercent = used)

    private val claude = UsageProviderDto(
        provider = "claude",
        displayName = "Claude",
        windows = listOf(window("5h", 23), window("7d", 12), window("7d", 8, scope = "Fable only")),
    )
    private val codex = UsageProviderDto(
        provider = "codex",
        displayName = "Codex",
        windows = listOf(window("7d", 53)),
    )

    @Test
    fun `一番消費しているウィンドウを選ぶ`() {
        assertEquals("5h", tightestWindow(claude)?.label)
        assertEquals(53, tightestWindow(codex)?.usedPercent)
    }

    @Test
    fun `ウィンドウがないプロバイダーは見出しに出さない`() {
        val empty = UsageProviderDto(provider = "codex", displayName = "Codex", error = "not logged in")
        assertNull(tightestWindow(empty))
        assertEquals("Claude 23%", usageHeadline(listOf(claude, empty)))
    }

    @Test
    fun `見出しはプロバイダーごとの最大消費率を並べる`() {
        assertEquals("Claude 23% · Codex 53%", usageHeadline(listOf(claude, codex)))
        assertEquals("", usageHeadline(emptyList()))
    }

    @Test
    fun `モデル別のウィンドウは対象を併記する`() {
        assertEquals("7d", windowTitle(window("7d", 12)))
        assertEquals("7d · Fable only", windowTitle(window("7d", 8, scope = "Fable only")))
    }
}
