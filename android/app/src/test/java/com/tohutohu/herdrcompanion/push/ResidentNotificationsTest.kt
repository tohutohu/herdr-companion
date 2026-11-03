package com.tohutohu.herdrcompanion.push

import android.app.Application
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], application = Application::class)
class ResidentNotificationsTest {
    private val context = RuntimeEnvironment.getApplication()

    @Test
    fun `同じ通知IDの再配送は二度表示しない`() {
        assertTrue(Notifications.markShown(context, "ntf_1"))
        assertFalse(Notifications.markShown(context, "ntf_1"))
        assertTrue(Notifications.markShown(context, "ntf_2"))
    }

    @Test
    fun `記録する通知IDは上限までで古いものから忘れる`() {
        repeat(70) { Notifications.markShown(context, "old_$it") }
        assertTrue(Notifications.markShown(context, "old_0"))
        assertFalse(Notifications.markShown(context, "old_69"))
    }

    @Test
    fun `常駐の判断待ちと失敗と変化はSessionsとは別のチャンネルで知らせる`() {
        assertEquals(Notifications.CHANNEL_RESIDENT_ATTENTION, Notifications.residentChannel("question"))
        assertEquals(Notifications.CHANNEL_RESIDENT_ERRORS, Notifications.residentChannel("failure"))
        assertEquals(Notifications.CHANNEL_RESIDENT, Notifications.residentChannel("change"))
    }
}
