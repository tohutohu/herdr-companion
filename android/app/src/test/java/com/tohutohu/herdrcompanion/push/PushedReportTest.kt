package com.tohutohu.herdrcompanion.push

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class PushedReportTest {
    private val base = mapOf(
        "sessionId" to "claude:s1",
        "status" to "completed",
        "provider" to "claude",
        "providerName" to "Claude Code",
        "project" to "my-project",
        "canSend" to "true",
    )

    @Test
    fun `通知に添えられた報告を読み取る`() {
        val push = pushedReport(
            base + ("report" to """{"id":"a1","role":"assistant","timestamp":"2026-10-21T01:02:03Z","blocks":[{"type":"text","text":"修正しました。"}]}"""),
        )!!
        assertEquals("claude:s1", push.sessionId)
        assertEquals("completed", push.status)
        assertEquals("Claude Code", push.providerName)
        assertEquals(true, push.canSend)
        assertEquals("a1", push.report.id)
        assertEquals("修正しました。", push.report.blocks.single().text)
    }

    @Test
    fun `報告がないか読めない通知は会話に何も足さない`() {
        assertNull(pushedReport(base))
        assertNull(pushedReport(base + ("report" to "{\"id\":")))
    }

    @Test
    fun `表示名のない古いGatewayの通知はプロバイダ名で代用する`() {
        val push = pushedReport(
            base - "providerName" + ("report" to """{"id":"a1","role":"assistant","timestamp":"2026-10-21T01:02:03Z"}"""),
        )!!
        assertEquals("claude", push.providerName)
    }
}
