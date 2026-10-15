package com.tohutohu.herdrcompanion.data

import java.io.ByteArrayOutputStream
import java.io.IOException
import java.io.InputStream

internal class UploadTooLargeException : IOException()

/** Reads at most [limit] + 1 bytes, including when the provider reports no size. */
internal fun InputStream.readUploadBytes(limit: Int, checkCancelled: () -> Unit = {}): ByteArray {
    require(limit >= 0)
    val output = ByteArrayOutputStream(minOf(limit, DEFAULT_BUFFER_SIZE))
    val buffer = ByteArray(DEFAULT_BUFFER_SIZE)
    var total = 0L
    while (true) {
        checkCancelled()
        val remaining = limit.toLong() + 1 - total
        val count = read(buffer, 0, minOf(buffer.size.toLong(), remaining).toInt())
        if (count == -1) break
        if (count == 0) {
            // Some content providers return zero from a bulk read. Still make
            // progress without spinning or reading beyond the same limit.
            val byte = read()
            if (byte == -1) break
            total++
            if (total > limit) throw UploadTooLargeException()
            output.write(byte)
        } else {
            total += count
            if (total > limit) throw UploadTooLargeException()
            output.write(buffer, 0, count)
        }
    }
    checkCancelled()
    return output.toByteArray()
}
