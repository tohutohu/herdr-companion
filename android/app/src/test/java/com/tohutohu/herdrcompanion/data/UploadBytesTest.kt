package com.tohutohu.herdrcompanion.data

import java.io.ByteArrayInputStream
import java.io.InputStream
import java.util.concurrent.CancellationException
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class UploadBytesTest {
    @Test
    fun `上限と同じサイズの添付を変更せず読み込める`() {
        val bytes = ByteArray(20 * 1024 * 1024) { (it % 251).toByte() }

        assertArrayEquals(bytes, bytes.inputStream().readUploadBytes(bytes.size))
    }

    @Test
    fun `空の添付を読み込める`() {
        assertArrayEquals(byteArrayOf(), byteArrayOf().inputStream().readUploadBytes(0))
    }

    @Test
    fun `サイズ不明の巨大ストリームも上限プラス一バイトで拒否する`() {
        val limit = 20 * 1024 * 1024
        var consumed = 0
        val unknownSize = object : InputStream() {
            // available() deliberately stays zero, as it can for a provider.
            override fun read(): Int {
                consumed++
                return 1
            }

            override fun read(buffer: ByteArray, offset: Int, length: Int): Int {
                consumed += length
                buffer.fill(1, offset, offset + length)
                return length
            }
        }

        assertThrows(UploadTooLargeException::class.java) { unknownSize.readUploadBytes(limit) }
        assertEquals(limit + 1, consumed)
    }

    @Test
    fun `一バイトだけ上限を超えた添付も拒否する`() {
        assertThrows(UploadTooLargeException::class.java) {
            ByteArray(33).inputStream().readUploadBytes(32)
        }
    }

    @Test
    fun `短い読み取りが繰り返されても全バイトを保持する`() {
        val bytes = ByteArray(99) { it.toByte() }
        val shortReads = object : ByteArrayInputStream(bytes) {
            override fun read(buffer: ByteArray, offset: Int, length: Int): Int =
                super.read(buffer, offset, minOf(3, length))
        }

        assertArrayEquals(bytes, shortReads.readUploadBytes(bytes.size))
    }

    @Test
    fun `ゼロバイトを返すプロバイダーでも停止せず読み込める`() {
        val bytes = byteArrayOf(0, 1, 2)
        val zeroReads = object : ByteArrayInputStream(bytes) {
            override fun read(buffer: ByteArray, offset: Int, length: Int): Int = 0
        }

        assertArrayEquals(bytes, zeroReads.readUploadBytes(bytes.size))
    }

    @Test
    fun `キャンセル後は次の読み取りを実行しない`() {
        var reads = 0
        val stream = object : ByteArrayInputStream(ByteArray(20_000)) {
            override fun read(buffer: ByteArray, offset: Int, length: Int): Int {
                reads++
                return super.read(buffer, offset, length)
            }
        }

        assertThrows(CancellationException::class.java) {
            stream.readUploadBytes(20_000) {
                if (reads > 0) throw CancellationException()
            }
        }
        assertEquals(1, reads)
    }
}
