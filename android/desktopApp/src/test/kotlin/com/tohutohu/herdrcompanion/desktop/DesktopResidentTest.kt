package com.tohutohu.herdrcompanion.desktop

import com.tohutohu.herdrcompanion.data.Settings
import com.tohutohu.herdrcompanion.data.api.ResidentTaskDto
import com.tohutohu.herdrcompanion.ui.resident.ResidentHomeState
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotEquals
import kotlin.test.assertNull
import kotlin.test.assertSame
import okhttp3.OkHttpClient

class DesktopResidentTest {
    @Test
    fun `接続先を変えると別のGatewayのタスクと選択を捨てる`() {
        var settings = Settings("http://127.0.0.1:1", "a")
        val state = DesktopResidentState({ settings }, OkHttpClient())
        val first = state.backend()
        assertSame(first, state.backend())
        state.home = ResidentHomeState(tasks = listOf(ResidentTaskDto(id = "task_1", name = "CI", provider = "claude")))
        state.pane = ResidentPane.Task("task_1")
        settings = Settings("http://127.0.0.1:2", "b")
        val second = state.backend()
        assertNotEquals(first.connectionKey, second.connectionKey)
        assertNull(state.home.tasks)
        assertEquals(ResidentPane.None, state.pane)
        state.close()
    }

    @Test
    fun `通知から開くと常駐の表示に切り替えてタスクを選ぶ`() {
        val state = DesktopResidentState({ Settings() }, OkHttpClient())
        state.openTask("task_9")
        assertEquals(DesktopTopTab.RESIDENT, state.topTab)
        assertEquals(ResidentPane.Task("task_9"), state.pane)
        state.close()
    }

    @Test
    fun `未読と判断待ちの数を見出しに出せる`() {
        val state = DesktopResidentState({ Settings() }, OkHttpClient())
        state.home = ResidentHomeState(tasks = listOf(
            ResidentTaskDto(id = "a", name = "a", provider = "claude", unread = 2),
            ResidentTaskDto(id = "b", name = "b", provider = "codex", unread = 1, state = "needs_attention"),
        ))
        assertEquals(3, state.unread)
        assertEquals(1, state.waiting)
        state.close()
    }
}
