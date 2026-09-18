package com.tohutohu.herdrmobile.data.api

import kotlinx.serialization.decodeFromString
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class DtoTest {
    @Test
    fun `プラン承認カードの種類と変更指示の選択肢名を読み込む`() {
        val block = GatewayApi.json.decodeFromString<BlockDto>(
            """
            {"type":"interaction","interaction":{"id":"toolu_plan","type":"questions","kind":"plan","state":"pending",
             "title":"Claude has a plan","supported":true,
             "questions":[{"id":"0","type":"select","question":"Would you like to proceed?",
               "options":[{"label":"Yes, auto-accept edits"},{"label":"No, keep planning"}],
               "allowOther":true,"otherLabel":"Tell Claude what to change"}]}}
            """.trimIndent(),
        )
        val ia = block.interaction!!
        assertEquals("plan", ia.kind)
        assertEquals("Tell Claude what to change", ia.questions.single().otherLabel)
    }

    @Test
    fun `古いゲートウェイの質問カードは種類も選択肢名もない`() {
        val q = GatewayApi.json.decodeFromString<QuestionDto>("""{"id":"0","type":"select","question":"Which?","allowOther":true}""")
        assertNull(q.otherLabel)
    }

    @Test
    fun `ワークスペース外のファイルは表示名つきで読み込む`() {
        val block = GatewayApi.json.decodeFromString<BlockDto>(
            """{"type":"file","path":"/Users/me/.claude/plans/plan-add-subtract.md","text":"plan-add-subtract.md","size":15}""",
        )
        assertEquals("plan-add-subtract.md", block.text)
        assertEquals("/Users/me/.claude/plans/plan-add-subtract.md", block.path)
    }
}
