package com.tohutohu.herdrcompanion.data.api

import java.io.ByteArrayInputStream
import java.io.IOException
import java.io.InputStream
import java.util.concurrent.CancellationException
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue
import okhttp3.MediaType.Companion.toMediaType
import okio.Buffer
import okio.Sink
import okio.Timeout
import okio.buffer

class StreamingUploadBodyTest {
    private fun body(size: Long? = null, checkCancelled: () -> Unit = {}, source: () -> InputStream) =
        StreamingUploadBody("application/octet-stream".toMediaType(), "file.bin", size, source, checkCancelled)

    private class GeneratedStream(private val size: Long) : InputStream() {
        var consumed = 0L
        var maximumRead = 0
        var closed = false
        override fun read(): Int = if (consumed < size) { consumed++; 0 } else -1
        override fun read(bytes: ByteArray, offset: Int, length: Int): Int {
            maximumRead = maxOf(maximumRead, length)
            if (consumed == size) return -1
            val n = minOf(length.toLong(), size - consumed).toInt()
            bytes.fill(0, offset, offset + n)
            consumed += n
            return n
        }
        override fun close() { closed = true }
    }

    private class CountingSink : Sink {
        var bytes = 0L
        override fun write(source: Buffer, byteCount: Long) { source.skip(byteCount); bytes += byteCount }
        override fun flush() = Unit
        override fun close() = Unit
        override fun timeout() = Timeout.NONE
    }

    @Test
    fun `500MiBちょうどを64KiB以下の読み取りで転送する`() {
        val source = GeneratedStream(MAX_UPLOAD_BYTES)
        val destination = CountingSink()
        val request = body(MAX_UPLOAD_BYTES) { source }
        destination.buffer().use { request.writeTo(it) }
        assertEquals(MAX_UPLOAD_BYTES, destination.bytes)
        assertEquals(MAX_UPLOAD_BYTES, source.consumed)
        assertTrue(source.maximumRead <= 64 * 1024)
        assertTrue(source.closed)
        assertEquals(MAX_UPLOAD_BYTES, request.contentLength())
        assertTrue(request.isOneShot())
    }

    @Test
    fun `サイズ不明の巨大添付は500MiBプラス一バイトで停止する`() {
        val source = GeneratedStream(Long.MAX_VALUE)
        val destination = CountingSink()
        val request = body { source }
        assertEquals(-1L, request.contentLength())
        assertFailsWith<IOException> { destination.buffer().use { request.writeTo(it) } }
        assertEquals(MAX_UPLOAD_BYTES + 1, source.consumed)
        assertEquals(MAX_UPLOAD_BYTES, destination.bytes)
        assertTrue(source.closed)
    }

    @Test
    fun `既知サイズが上限を超える場合はファイルを開かない`() {
        var opens = 0
        assertFailsWith<IOException> { body(MAX_UPLOAD_BYTES + 1) { opens++; ByteArrayInputStream(byteArrayOf()) } }
        assertEquals(0, opens)
    }

    @Test
    fun `キャンセル後は追加読み取りせずストリームを閉じる`() {
        val source = GeneratedStream(MAX_UPLOAD_BYTES)
        val request = body(checkCancelled = { if (source.consumed > 0) throw CancellationException() }) { source }
        val failure = assertFailsWith<IOException> { CountingSink().buffer().use { request.writeTo(it) } }
        assertTrue(failure.cause is CancellationException)
        assertEquals(64 * 1024L, source.consumed)
        assertTrue(source.closed)
    }

    @Test
    fun `プロバイダーの権限エラーはOkHttpスレッドの未捕捉例外にしない`() {
        val failure = assertFailsWith<IOException> {
            CountingSink().buffer().use { body { throw SecurityException("permission expired") }.writeTo(it) }
        }
        assertTrue(failure.cause is SecurityException)
    }

    @Test
    fun `メタデータ取得後に縮小または拡大したファイルは成功扱いにしない`() {
        for (length in listOf(2L, 4L)) {
            val source = GeneratedStream(length)
            assertFailsWith<IOException> { CountingSink().buffer().use { body(3) { source }.writeTo(it) } }
            assertTrue(source.closed)
        }
    }

    @Test
    fun `短い読み取りやゼロ読み取りでも内容を保持する`() {
        val bytes = byteArrayOf(0, 1, 2, 3)
        val source = object : ByteArrayInputStream(bytes) {
            var zero = true
            override fun read(buffer: ByteArray, offset: Int, length: Int): Int {
                zero = !zero
                return if (zero) 0 else super.read(buffer, offset, minOf(length, 1))
            }
        }
        val destination = Buffer()
        body { source }.writeTo(destination)
        assertContentEquals(bytes, destination.readByteArray())
        val empty = Buffer()
        body(0) { byteArrayOf().inputStream() }.writeTo(empty)
        assertEquals(0L, empty.size)
    }
}
