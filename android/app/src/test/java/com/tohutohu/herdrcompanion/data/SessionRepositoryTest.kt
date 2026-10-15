package com.tohutohu.herdrcompanion.data

import android.app.Application
import androidx.room.Room
import com.tohutohu.herdrcompanion.data.api.GatewayApi
import com.tohutohu.herdrcompanion.data.db.AppDatabase
import com.tohutohu.herdrcompanion.data.db.MessageEntity
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.OkHttpClient
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config
import org.robolectric.annotation.SQLiteMode

@RunWith(RobolectricTestRunner::class)
@Config(application = Application::class, sdk = [35])
@SQLiteMode(SQLiteMode.Mode.NATIVE)
class SessionRepositoryTest {
    @Test
    fun `通知と画面の同時取得が古い応答で会話を巻き戻さない`() = concurrentRefresh(reload = false)

    @Test
    fun `全再読込も通常の増分取得と同じセッションロックを使う`() = concurrentRefresh(reload = true)

    private fun concurrentRefresh(reload: Boolean) = runBlocking(Dispatchers.IO) {
        val firstArrived = CountDownLatch(1)
        val releaseFirst = CountDownLatch(1)
        val secondStarted = CountDownLatch(1)
        val requests = AtomicInteger()
        val server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                val index = requests.incrementAndGet()
                if (index == 1) {
                    firstArrived.countDown()
                    check(releaseFirst.await(5, TimeUnit.SECONDS))
                }
                val ids = if (index == 1) listOf("m1") else listOf("m1", "m2")
                val messages = ids.joinToString { """{"id":"$it","role":"assistant","timestamp":"2026-10-15T00:00:00Z","blocks":[]}""" }
                return MockResponse.Builder().body("""{"session":{"id":"s","provider":"claude","status":"running","updatedAt":"2026-10-15T00:00:00Z"},"messages":[$messages],"findings":[]}""").build()
            }
        }
        server.start()
        val db = Room.inMemoryDatabaseBuilder(RuntimeEnvironment.getApplication(), AppDatabase::class.java).build()
        try {
            db.messages().upsert(listOf(MessageEntity("s", "m1", 0, "assistant", 0, "[]")))
            val repo = SessionRepository(db, GatewayApi(OkHttpClient()) { Settings(server.url("/").toString(), "test") })
            val first = async { repo.refreshMessages("s") }
            assertTrue(firstArrived.await(5, TimeUnit.SECONDS))
            val second = async {
                secondStarted.countDown()
                if (reload) repo.reloadMessages("s", emptyList()) else repo.refreshMessages("s")
            }
            assertTrue(secondStarted.await(5, TimeUnit.SECONDS))
            // The server already has the first request. A second caller must
            // wait before reading an anchor or issuing its HTTP request.
            Thread.sleep(200)
            val beforeRelease = requests.get()
            releaseFirst.countDown()
            awaitAll(first, second)
            assertEquals("one request at a time for this session", 1, beforeRelease)
            assertEquals(listOf("m1", "m2"), db.messages().observe("s").first().map { it.id })
        } finally {
            releaseFirst.countDown()
            db.close()
            server.close()
        }
    }
}
