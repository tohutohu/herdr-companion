package com.tohutohu.herdrcompanion.data.api

import java.io.IOException
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertTrue
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.SocketEffect
import okhttp3.Call
import okhttp3.Connection
import okhttp3.EventListener
import okhttp3.OkHttpClient
import okhttp3.Request

class CallAwaitTest {
    @Test
    fun `応答待ちのキャンセルでHTTP呼出しとソケットも終了する`() = runBlocking {
        val failed = CountDownLatch(1)
        val client = OkHttpClient.Builder().eventListener(object : EventListener() {
            override fun callFailed(call: Call, ioe: IOException) { failed.countDown() }
        }).build()
        MockWebServer().use { server ->
            server.enqueue(MockResponse.Builder().onResponseStart(SocketEffect.Stall).build())
            server.start()
            val call = client.newCall(Request.Builder().url(server.url("/")).build())
            val job = async(Dispatchers.IO) { call.awaitBody { it.body.string() } }
            assertEquals("GET", server.takeRequest(5, TimeUnit.SECONDS)!!.method)
            withTimeout(2_000) { job.cancelAndJoin() }
            assertTrue(call.isCanceled())
            assertTrue(failed.await(2, TimeUnit.SECONDS))
        }
    }

    @Test
    fun `本文を途中まで読んでいる時もキャンセルで接続を閉じる`() = runBlocking {
        val bodyStarted = CountDownLatch(1)
        val failed = CountDownLatch(1)
        val client = OkHttpClient.Builder().eventListener(object : EventListener() {
            override fun callFailed(call: Call, ioe: IOException) { failed.countDown() }
        }).build()
        MockWebServer().use { server ->
            server.enqueue(MockResponse.Builder().body("data").onResponseBody(SocketEffect.Stall).build())
            server.start()
            val call = client.newCall(Request.Builder().url(server.url("/")).build())
            val job = async(Dispatchers.IO) {
                call.awaitBody {
                    bodyStarted.countDown()
                    it.body.string()
                }
            }
            assertTrue(bodyStarted.await(5, TimeUnit.SECONDS))
            withTimeout(2_000) { job.cancelAndJoin() }
            assertTrue(call.isCanceled())
            assertTrue(failed.await(2, TimeUnit.SECONDS))
        }
    }

    @Test
    fun `本文処理が失敗してもレスポンスを閉じる`() = runBlocking {
        val released = CountDownLatch(1)
        val client = OkHttpClient.Builder().eventListener(object : EventListener() {
            override fun connectionReleased(call: Call, connection: Connection) { released.countDown() }
        }).build()
        MockWebServer().use { server ->
            server.enqueue(MockResponse.Builder().body("data").build())
            server.start()
            val call = client.newCall(Request.Builder().url(server.url("/")).build())
            val failure = runCatching { call.awaitBody<String> { throw IllegalStateException("bad data") } }.exceptionOrNull()
            assertIs<IllegalStateException>(failure)
            assertTrue(released.await(2, TimeUnit.SECONDS))
        }
    }
}
