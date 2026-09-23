package com.tohutohu.herdrcompanion.desktop

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

class DesktopHerdrTerminalTest {
    @Test
    fun `pane getの結果からterminal_idを取り出す`() {
        val output = """{"id":"cli:pane:get","result":{"pane":{"pane_id":"w1:p1","terminal_id":"term_65c1ce31f51bb1","workspace_id":"w1"}}}"""

        assertEquals("term_65c1ce31f51bb1", parseTerminalId(output))
    }

    @Test
    fun `ペインが見つからないエラーや壊れた出力ではnullを返す`() {
        assertNull(parseTerminalId("""{"error":{"code":"pane_not_found","message":"pane w9:p9 not found"},"id":"cli:pane:get"}"""))
        assertNull(parseTerminalId("Error: not json"))
        assertNull(parseTerminalId(""))
    }

    @Test
    fun `attachスクリプトは既定セッションのターミナルにexecで接続する`() {
        val script = attachScript("/Users/me/.local/bin/herdr", "term_1")

        assertTrue(script.startsWith("#!/bin/sh\nrm -f \"$0\"\nrmdir \"$(dirname \"$0\")\" 2>/dev/null\n"))
        assertTrue("unset HERDR_SOCKET_PATH HERDR_SESSION HERDR_PANE_ID\n" in script)
        assertTrue(script.endsWith("exec '/Users/me/.local/bin/herdr' terminal attach 'term_1'\n"))
    }

    @Test
    fun `attachスクリプトは引数のシングルクォートをエスケープする`() {
        val script = attachScript("/Users/o'neil/bin/herdr", "term_'x")

        assertTrue("exec '/Users/o'\\''neil/bin/herdr' terminal attach 'term_'\\''x'" in script)
    }
}
