package com.tohutohu.herdrcompanion.data.api

import java.io.IOException
import java.io.InputStream
import okhttp3.MediaType
import okhttp3.RequestBody
import okio.BufferedSink

const val MAX_UPLOAD_BYTES = 500L * 1024 * 1024

/** The source is opened only when OkHttp sends it, and is always closed after the attempt. */
internal class StreamingUploadBody(
    private val type: MediaType,
    private val filename: String,
    private val size: Long?,
    private val openStream: () -> InputStream,
    private val checkCancelled: () -> Unit,
) : RequestBody() {
    init {
        require(size == null || size >= 0) { "Invalid attachment size" }
        if (size != null && size > MAX_UPLOAD_BYTES) throw tooLarge()
    }

    override fun contentType() = type
    override fun contentLength() = size ?: -1L

    // Content providers may not reopen, and retrying a partially sent upload
    // would create a second temporary file. Let the user retry explicitly.
    override fun isOneShot() = true

    override fun writeTo(sink: BufferedSink) {
        try {
            copyTo(sink)
        } catch (e: IOException) {
            throw e
        } catch (e: Exception) {
            // OkHttp treats IOException as a failed call. Throwing a provider
            // SecurityException or coroutine cancellation from writeTo would
            // instead escape its dispatcher thread as an uncaught exception.
            throw IOException(e.message ?: "Could not read $filename", e)
        }
    }

    private fun copyTo(sink: BufferedSink) {
        checkCancelled()
        openStream().use { input ->
            val buffer = ByteArray(64 * 1024)
            var total = 0L
            while (true) {
                checkCancelled()
                // Unknown-size documents also stop at the limit plus one byte.
                val count = minOf(buffer.size.toLong(), MAX_UPLOAD_BYTES + 1 - total).toInt()
                var n = input.read(buffer, 0, count)
                if (n == 0) {
                    val byte = input.read()
                    if (byte < 0) break
                    buffer[0] = byte.toByte()
                    n = 1
                }
                if (n < 0) break
                total += n
                if (total > MAX_UPLOAD_BYTES) throw tooLarge()
                if (size != null && total > size) throw sizeChanged()
                sink.write(buffer, 0, n)
            }
            checkCancelled()
            if (size != null && total != size) throw sizeChanged()
        }
    }

    private fun tooLarge() = IOException("$filename is larger than 500 MiB")
    private fun sizeChanged() = IOException("$filename changed size while uploading; pick the file again")
}
