package com.tohutohu.herdrmobile.ui.usage

import com.tohutohu.herdrmobile.data.api.UsageProviderDto
import com.tohutohu.herdrmobile.data.api.UsageWindowDto
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class UsageFormatTest {
    private fun window(label: String, used: Int, scope: String? = null, resetsAt: String? = null) =
        UsageWindowDto(key = label, label = label, scope = scope, usedPercent = used, resetsAt = resetsAt)

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
    fun `折りたたみ見出しにリセットまでの時間を併記する`() {
        val providers = listOf(
            claude.copy(windows = listOf(window("5h", 23, resetsAt = "2026-09-18T02:30:00Z"))),
            codex.copy(windows = listOf(window("7d", 53, resetsAt = "2026-09-24T04:00:00Z"))),
        )
        val now = java.time.Instant.parse("2026-09-18T00:15:00Z").toEpochMilli()

        assertEquals("Claude 23% (in 2h 15m) · Codex 53% (in 6d 3h)", usageHeadlineWithReset(providers, now))
    }

    @Test
    fun `リセットまでの時間を分と時と日に丸めて表示する`() {
        val now = java.time.Instant.parse("2026-09-18T00:00:00Z").toEpochMilli()

        assertEquals("in 45m", resetCountdown("2026-09-18T00:45:00Z", now))
        assertEquals("in 2h 15m", resetCountdown("2026-09-18T02:15:00Z", now))
        assertEquals("in 2d 3h", resetCountdown("2026-09-20T03:15:00Z", now))
        assertEquals("now", resetCountdown("2026-09-17T23:59:00Z", now))
        assertEquals(null, resetCountdown("not a timestamp", now))
    }

    @Test
    fun `モデル別のウィンドウは対象を併記する`() {
        assertEquals("7d", windowTitle(window("7d", 12)))
        assertEquals("7d · Fable only", windowTitle(window("7d", 8, scope = "Fable only")))
    }
}
