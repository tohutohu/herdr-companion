package com.tohutohu.herdrcompanion.push

import com.tohutohu.herdrcompanion.data.api.Status
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class NotificationsTest {
    @Test
    fun `返信欄は送信できるセッションに出し回答待ちには出さない`() {
        assertTrue(Notifications.canReply(Status.COMPLETED, canSend = true))
        assertTrue(Notifications.canReply(Status.FAILED, canSend = true))
        // 質問や承認は専用の回答UIで答えるため、自由入力は受け付けない
        assertFalse(Notifications.canReply(Status.WAITING_INPUT, canSend = true))
        assertFalse(Notifications.canReply(Status.WAITING_APPROVAL, canSend = true))
        // 終了済みなど送信先のないセッション
        assertFalse(Notifications.canReply(Status.COMPLETED, canSend = false))
    }

    @Test
    fun `通知チャンネルは状態ごとに分かれる`() {
        assertTrue(Notifications.channelFor(Status.COMPLETED) == Notifications.CHANNEL_COMPLETED)
        assertTrue(Notifications.channelFor(Status.WAITING_INPUT) == Notifications.CHANNEL_ATTENTION)
        assertTrue(Notifications.channelFor(Status.FAILED) == Notifications.CHANNEL_ERRORS)
    }
}
