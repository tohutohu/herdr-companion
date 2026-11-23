package com.tohutohu.herdrcompanion

import android.app.Application
import android.content.Intent
import android.net.Uri
import com.tohutohu.herdrcompanion.data.SessionLink
import com.tohutohu.herdrcompanion.push.Notifications
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], application = Application::class)
class LaunchTargetTest {
    private val context = RuntimeEnvironment.getApplication()

    @Test
    fun `セッション通知のタップは通知のセッションを開く`() {
        val intent = Notifications.sessionTapIntent(context, "claude:s2", "connection")

        assertEquals(LaunchTarget.SessionNotification("claude:s2"), LaunchTarget.of(intent, "connection", configured = true))
    }

    @Test
    fun `常駐通知のタップは通知の常駐タスクを開く`() {
        val intent = Notifications.residentTapIntent(context, "task_1", "connection")

        assertEquals(LaunchTarget.ResidentNotification("task_1"), LaunchTarget.of(intent, "connection", configured = true))
    }

    @Test
    fun `再ペアリング前の通知は別のGatewayのものなので開かない`() {
        val intent = Notifications.sessionTapIntent(context, "claude:s2", "old-connection")

        assertNull(LaunchTarget.of(intent, "connection", configured = true))
    }

    @Test
    fun `hcのセッションリンクは接続IDなしで開く`() {
        val intent = Intent(Intent.ACTION_VIEW, Uri.parse(SessionLink.url("codex:t1")))

        assertEquals(LaunchTarget.Link("codex:t1"), LaunchTarget.of(intent, "connection", configured = true))
    }

    @Test
    fun `未設定のあいだはリンクでセッションを開かない`() {
        val intent = Intent(Intent.ACTION_VIEW, Uri.parse(SessionLink.url("codex:t1")))

        assertNull(LaunchTarget.of(intent, "connection", configured = false))
    }

    @Test
    fun `リンクに通知のextraを付けても通知としては扱わない`() {
        val intent = Intent(Intent.ACTION_VIEW, Uri.parse("https://example.com/"))
            .putExtra(Notifications.EXTRA_SESSION_ID, "claude:s2")
            .putExtra(Notifications.EXTRA_CONNECTION_ID, "connection")

        assertNull(LaunchTarget.of(intent, "connection", configured = true))
    }
}
