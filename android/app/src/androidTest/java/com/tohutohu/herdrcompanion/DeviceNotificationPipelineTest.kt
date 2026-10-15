package com.tohutohu.herdrcompanion

import android.Manifest
import android.app.NotificationManager
import android.app.RemoteInput
import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.os.SystemClock
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.rule.GrantPermissionRule
import androidx.work.WorkManager
import com.tohutohu.herdrcompanion.push.Notifications
import com.tohutohu.herdrcompanion.push.PushService
import com.tohutohu.herdrcompanion.test.DeviceTestActivity
import com.google.firebase.messaging.FcmBroadcastProcessor
import java.io.File
import java.util.UUID
import java.util.concurrent.CountDownLatch
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.tasks.await
import org.junit.After
import org.junit.Assert.*
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/** Android service/PendingIntent/Worker/SQLite integration, with synthetic push input. */
@RunWith(AndroidJUnit4::class)
class DeviceNotificationPipelineTest {
    // Fail before permission rules, cleanup or settings writes on any other APK.
    private val context: Context = InstrumentationRegistry.getInstrumentation().targetContext.also {
        check(it.packageName.endsWith(".devicetest")) { "Refusing to modify a non-test installation" }
    }
    @get:Rule val permission = GrantPermissionRule.grant(Manifest.permission.POST_NOTIFICATIONS)
    @get:Rule val compose = createEmptyComposeRule()
    private lateinit var fixture: DeviceGatewayFixture
    private var host: ActivityScenario<DeviceTestActivity>? = null
    private var main: ActivityScenario<MainActivity>? = null
    private val app get() = context.applicationContext as HerdrMobileApp
    private val work get() = WorkManager.getInstance(context)
    private val notifications get() = context.getSystemService(NotificationManager::class.java)

    @Before fun prepareIsolatedApp() {
        fixture = DeviceGatewayFixture()
        work.cancelAllWork().result.get()
        File(context.filesDir, "notification-replies").deleteRecursively()
        runBlocking(Dispatchers.IO) {
            app.container.db.clearAllTables()
            app.container.settings.save(fixture.settings)
        }
        notifications.cancelAll()
        Notifications.createChannels(context)
        DeviceTestActivity.content = {}
        host = ActivityScenario.launch(DeviceTestActivity::class.java)
    }

    @After fun cleanIsolatedApp() {
        main?.close()
        host?.close()
        context.stopService(Intent(context, PushService::class.java))
        work.cancelAllWork().result.get()
        notifications.cancelAll()
        if (::fixture.isInitialized) fixture.close()
    }

    @Test fun 合成pushでServiceからRoomへ取得し通知PendingIntentで詳細へ移動できる() {
        // Feed the SDK's service queue, not startService directly (the Firebase
        // service reads its next event from that queue). This is synthetic SDK
        // dispatch, not delivery over the real Firebase network transport.
        runBlocking { FcmBroadcastProcessor(context).process(Intent("com.google.android.c2dm.intent.RECEIVE")
            .setPackage(context.packageName)
            .putExtra("google.message_id", UUID.randomUUID().toString())
            .putExtra("from", "device-test")
            .putExtra("sessionId", fixture.sessionId)
            .putExtra("gatewayId", fixture.gatewayId)
            .putExtra("status", "completed")
            .putExtra("title", "Device test notification")
            .putExtra("body", "Synthetic push fixture")
            .putExtra("canSend", "true")).await() }
        await("push prefetch reached real Room") { cachedTexts().contains(fixture.transcript) }
        val notification = currentNotification()
        // ActivityScenario matches lifecycle events by Intent.filterEquals.
        // onNewIntent stores the notification's ACTION_VIEW intent; launch with
        // the same shape so recreation/cleanup remain tracked. No session extra
        // is provided here: the actual PendingIntent must perform navigation.
        main = ActivityScenario.launch(Intent(context, MainActivity::class.java).setAction(Intent.ACTION_VIEW))
        notification.contentIntent.send()
        compose.waitUntil(20_000) {
            compose.onAllNodesWithText(fixture.transcript, substring = true).fetchSemanticsNodes().isNotEmpty()
        }
        main!!.recreate()
        compose.waitUntil(20_000) {
            compose.onAllNodesWithText(fixture.transcript, substring = true).fetchSemanticsNodes().isNotEmpty()
        }
    }

    @Test fun 実RemoteInputの長文をHTTPへ送り再取得したRoomで本文が一致する() {
        val reply = "日本語の長文🙂\n".repeat(3_000).trim()
        sendNotificationReply(reply)
        await("reply accepted") { fixture.accepted.size == 1 }
        assertEquals(reply, fixture.accepted.single().text)
        assertFalse(fixture.accepted.single().requestId.isNullOrBlank())
        runBlocking { app.container.repository.refreshMessages(fixture.sessionId) }
        assertTrue(cachedTexts().contains(reply))
        await("successful private payload removed") { File(context.filesDir, "notification-replies").listFiles().orEmpty().none { it.name.endsWith(".json") } }
    }

    @Test fun 一時HTTP障害から同一返信IDで再試行し二重メッセージを作らない() {
        fixture.nextPostStatus.set(503)
        sendNotificationReply("一時障害から復帰🙂")
        await("first attempt failed") { fixture.attempts.size == 1 }
        assertTrue(fixture.accepted.isEmpty())
        await("WorkManager backoff retried", 60_000) { fixture.accepted.size == 1 }
        assertEquals(2, fixture.attempts.size)
        assertEquals(fixture.attempts.first().requestId, fixture.attempts.last().requestId)
        assertEquals("一時障害から復帰🙂", fixture.accepted.single().text)
    }

    @Test fun 先行返信が恒久エラーでも既に待機した後続返信は実行される() {
        val gate = CountDownLatch(1)
        fixture.postGate = gate
        fixture.nextPostStatus.set(400)
        sendNotificationReply("先の返信")
        await("first POST entered its gate") { fixture.attempts.size == 1 }
        try {
            sendNotificationReply("後の返信")
            await("second reply already appended before first failure") {
                work.getWorkInfosForUniqueWork("reply-${fixture.connectionId}-${fixture.sessionId}").get().size == 2
            }
        } finally {
            gate.countDown()
        }
        await("second reply was not poisoned by failed predecessor") { fixture.attempts.size == 2 && fixture.accepted.size == 1 }
        assertEquals(listOf("先の返信", "後の返信"), fixture.attempts.map { it.text })
        assertEquals("後の返信", fixture.accepted.single().text)
    }

    private fun sendNotificationReply(text: String) {
        Notifications.show(context, fixture.sessionId, "completed", "Device reply fixture", "Fixture notification", canSend = true)
        await("RemoteInput action posted") {
            notifications.activeNotifications.firstOrNull { it.id == fixture.sessionId.hashCode() }
                ?.notification?.actions?.any { !it.remoteInputs.isNullOrEmpty() } == true
        }
        val action = currentNotification().actions.single { !it.remoteInputs.isNullOrEmpty() }
        val fillIn = Intent()
        RemoteInput.addResultsToIntent(action.remoteInputs, fillIn, Bundle().apply { putCharSequence(Notifications.KEY_REPLY, text) })
        action.actionIntent.send(context, 0, fillIn)
    }

    private fun currentNotification(): android.app.Notification {
        await("test notification posted") { notifications.activeNotifications.any { it.id == fixture.sessionId.hashCode() } }
        return notifications.activeNotifications.first { it.id == fixture.sessionId.hashCode() }.notification
    }

    private fun cachedTexts(): List<String> = runBlocking(Dispatchers.IO) {
        app.container.repository.observeMessages(fixture.sessionId).first().flatMap { it.blocks.mapNotNull { block -> block.text } }
    }

    private fun await(description: String, timeout: Long = 20_000, condition: () -> Boolean) {
        val deadline = SystemClock.elapsedRealtime() + timeout
        while (SystemClock.elapsedRealtime() < deadline) {
            if (condition()) return
            SystemClock.sleep(50)
        }
        throw AssertionError("Timed out: $description")
    }
}
