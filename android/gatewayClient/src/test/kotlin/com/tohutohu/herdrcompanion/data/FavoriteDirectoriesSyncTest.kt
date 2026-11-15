package com.tohutohu.herdrcompanion.data

import com.tohutohu.herdrcompanion.data.api.GatewayApi
import com.tohutohu.herdrcompanion.data.api.GatewayException
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.OkHttpClient

class FavoriteDirectoriesSyncTest {
    private class MemoryCache(var paths: List<String>) : FavoriteDirectoriesSync.Cache {
        val written = mutableListOf<List<String>>()
        override suspend fun read() = paths
        override suspend fun write(paths: List<String>) {
            this.paths = paths
            written += paths
        }
    }

    private fun json(code: Int, body: String) = MockResponse.Builder().code(code).body(body).build()

    private fun sync(server: MockWebServer, cache: MemoryCache) =
        FavoriteDirectoriesSync(GatewayApi(OkHttpClient()) { Settings(server.url("/").toString(), "secret") }, cache)

    @Test
    fun `Gatewayに一覧がなければ端末のお気に入りを並び順ごと取り込む`() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            server.enqueue(json(200, """{"paths":[],"saved":false}"""))
            server.enqueue(json(200, """{"paths":["/w/bot","/w/app"],"saved":true,"imported":true}"""))
            val cache = MemoryCache(listOf("/w/bot", "/w/app"))
            assertEquals(listOf("/w/bot", "/w/app"), sync(server, cache).refresh())
            assertEquals("/v1/directories/favorites", server.takeRequest().url.encodedPath)
            val import = server.takeRequest()
            assertEquals("/v1/directories/favorites/import", import.url.encodedPath)
            assertEquals("""{"paths":["/w/bot","/w/app"]}""", import.body?.utf8())
        }
    }

    @Test
    fun `Gatewayの一覧があれば端末のキャッシュを置き換える`() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            server.enqueue(json(200, """{"paths":["/w/a","/w/b"],"saved":true}"""))
            val cache = MemoryCache(listOf("/w/old"))
            sync(server, cache).refresh()
            assertEquals(listOf("/w/a", "/w/b"), cache.paths)
            assertEquals(1, server.requestCount)
        }
    }

    @Test
    fun `お気に入りに対応しない旧Gatewayでは端末だけで切り替える`() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            server.enqueue(json(404, "404 page not found"))
            val cache = MemoryCache(listOf("/w/a"))
            assertEquals(listOf("/w/a", "/w/b"), sync(server, cache).toggle("/w/b"))
            assertEquals(listOf("/w/a", "/w/b"), cache.paths)
            assertEquals(1, server.requestCount)
        }
    }

    @Test
    fun `切り替えは先に画面へ反映しGatewayの応答で確定する`() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            server.enqueue(json(200, """{"paths":["/w/a"],"saved":true}"""))
            server.enqueue(json(200, """{"paths":["/w/a","/w/b"],"saved":true}"""))
            server.enqueue(json(200, """{"paths":["/w/b"],"saved":true}"""))
            val cache = MemoryCache(listOf("/w/a"))
            val sync = sync(server, cache)
            sync.refresh()
            server.takeRequest()
            cache.written.clear()
            sync.toggle("/w/b")
            assertEquals(listOf(listOf("/w/a", "/w/b"), listOf("/w/a", "/w/b")), cache.written)
            val add = server.takeRequest()
            assertEquals("POST", add.method)
            assertEquals("""{"path":"/w/b"}""", add.body?.utf8())
            sync.toggle("/w/a")
            val remove = server.takeRequest()
            assertEquals("DELETE", remove.method)
            assertEquals("/w/a", remove.url.queryParameter("path"))
            assertEquals(listOf("/w/b"), cache.paths)
        }
    }

    @Test
    fun `Gatewayが拒否した変更は元に戻す`() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            server.enqueue(json(200, """{"paths":["/w/a"],"saved":true}"""))
            server.enqueue(json(403, """{"error":"path is outside the workspace"}"""))
            val cache = MemoryCache(listOf("/w/a"))
            val sync = sync(server, cache)
            sync.refresh()
            assertFailsWith<GatewayException> { sync.toggle("/etc") }
            assertEquals(listOf("/w/a"), cache.paths)
        }
    }

    @Test
    fun `並べ替えは他の端末で増えたお気に入りを消さない`() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            server.enqueue(json(200, """{"paths":["/w/a","/w/b","/w/c"],"saved":true}"""))
            server.enqueue(json(200, """{"paths":["/w/b","/w/a","/w/c"],"saved":true}"""))
            val cache = MemoryCache(emptyList())
            val sync = sync(server, cache)
            sync.refresh()
            server.takeRequest()
            sync.reorder(listOf("/w/b", "/w/a"))
            val put = server.takeRequest()
            assertEquals("PUT", put.method)
            assertEquals("""{"paths":["/w/b","/w/a","/w/c"]}""", put.body?.utf8())
        }
    }

    @Test
    fun `同期前の切り替えはGatewayの一覧に対して行う`() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            server.enqueue(json(200, """{"paths":["/w/x"],"saved":true}"""))
            server.enqueue(json(200, """{"paths":["/w/x","/w/a"],"saved":true}"""))
            val cache = MemoryCache(listOf("/w/a"))
            // "/w/a" is a favorite only in the stale cache, so the gateway gets an add.
            assertEquals(listOf("/w/x", "/w/a"), sync(server, cache).toggle("/w/a"))
            server.takeRequest()
            assertEquals("POST", server.takeRequest().method)
        }
    }
}
