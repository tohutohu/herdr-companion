package com.tohutohu.herdrcompanion.ui.detail

import com.tohutohu.herdrcompanion.data.Message
import com.tohutohu.herdrcompanion.data.api.BlockDto
import com.tohutohu.herdrcompanion.data.api.InteractionDto
import com.tohutohu.herdrcompanion.data.api.ReloadFindingDto
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class ReloadNoticeTest {
    private fun interaction(id: String, state: String) =
        BlockDto(type = "interaction", interaction = InteractionDto(id = id, type = "questions", state = state))

    @Test
    fun `表示中の未回答の質問だけを再読込の確認対象にする`() {
        val messages = listOf(
            Message("m1", "assistant", 0, listOf(BlockDto(type = "text", text = "hi"), interaction("q1", "answered"))),
            Message("m2", "assistant", 0, listOf(interaction("q2", "pending"), interaction("q3", "pending"))),
        )
        assertEquals(listOf("q2", "q3"), pendingInteractionIds(messages))
    }

    @Test
    fun `取りこぼしがなければその旨だけを知らせる`() {
        val notice = reloadNotice(emptyList())
        assertFalse(notice.missed)
        assertFalse(notice.openTerminal)
    }

    @Test
    fun `質問を捕捉できていなければターミナルへの導線を付ける`() {
        val notice = reloadNotice(listOf(ReloadFindingDto("not_shown"), ReloadFindingDto("uncaptured")))
        assertTrue(notice.missed)
        assertTrue(notice.openTerminal)
        assertTrue("not showing" in notice.text && "not captured" in notice.text)
    }

    @Test
    fun `未知の種類はGatewayの説明文で知らせる`() {
        val notice = reloadNotice(listOf(ReloadFindingDto("future", "Something new")))
        assertTrue(notice.missed)
        assertTrue(notice.text.startsWith("Something new"))
    }
}
