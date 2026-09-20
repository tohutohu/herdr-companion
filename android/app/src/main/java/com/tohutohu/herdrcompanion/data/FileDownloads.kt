package com.tohutohu.herdrcompanion.data

import android.content.ActivityNotFoundException
import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.provider.MediaStore
import android.webkit.MimeTypeMap
import com.tohutohu.herdrcompanion.data.api.GatewayApi
import java.io.File
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

sealed interface DownloadState {
    data object Idle : DownloadState
    data class Running(val bytes: Long, val total: Long) : DownloadState
    data class Done(val uri: Uri, val mimeType: String) : DownloadState
    data class Failed(val message: String) : DownloadState
}

/**
 * Saves gateway files into the public Downloads collection. Downloads run in
 * the app scope so they continue when the viewer screen is closed.
 */
class FileDownloads(
    private val context: Context,
    private val api: GatewayApi,
    private val scope: CoroutineScope,
) {
    private val states = mutableMapOf<String, MutableStateFlow<DownloadState>>()

    private fun key(sessionId: String, path: String) = "$sessionId\u0000$path"

    fun state(sessionId: String, path: String): StateFlow<DownloadState> =
        synchronized(states) { states.getOrPut(key(sessionId, path)) { MutableStateFlow(DownloadState.Idle) } }.asStateFlow()

    fun imageState(url: String): StateFlow<DownloadState> =
        synchronized(states) { states.getOrPut(imageKey(url)) { MutableStateFlow(DownloadState.Idle) } }.asStateFlow()

    fun start(sessionId: String, path: String, name: String, size: Long) {
        val flow = synchronized(states) { states.getOrPut(key(sessionId, path)) { MutableStateFlow(DownloadState.Idle) } }
        if (flow.value is DownloadState.Running) return
        flow.value = DownloadState.Running(0, size)
        scope.launch {
            val resolver = context.contentResolver
            val mimeType = mimeTypeFor(name)
            val values = ContentValues().apply {
                put(MediaStore.Downloads.DISPLAY_NAME, name)
                put(MediaStore.Downloads.MIME_TYPE, mimeType)
                put(MediaStore.Downloads.IS_PENDING, 1)
            }
            val uri = resolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values)
            if (uri == null) {
                flow.value = DownloadState.Failed("Could not create the download file")
                return@launch
            }
            try {
                val out = resolver.openOutputStream(uri) ?: error("Could not open the download file")
                out.use { api.downloadFile(sessionId, path, it) { n -> flow.value = DownloadState.Running(n, size) } }
                resolver.update(uri, ContentValues().apply { put(MediaStore.Downloads.IS_PENDING, 0) }, null, null)
                flow.value = DownloadState.Done(uri, mimeType)
            } catch (e: Throwable) {
                resolver.delete(uri, null, null)
                flow.value = DownloadState.Failed(e.message ?: e.toString())
                if (e is kotlinx.coroutines.CancellationException) throw e
            }
        }
    }

    /** Downloads an inline image into the public Downloads collection. */
    fun startImage(url: String) {
        val flow = synchronized(states) { states.getOrPut(imageKey(url)) { MutableStateFlow(DownloadState.Idle) } }
        if (flow.value is DownloadState.Running) return
        flow.value = DownloadState.Running(0, 0)
        scope.launch {
            val temp = try {
                File.createTempFile("herdr-image-", ".download", context.cacheDir)
            } catch (e: Throwable) {
                flow.value = DownloadState.Failed(e.message ?: e.toString())
                return@launch
            }
            var uri: Uri? = null
            try {
                var total = 0L
                val metadata = temp.outputStream().use { output ->
                    api.downloadUrl(
                        rawUrl = url,
                        out = output,
                        onHeaders = { headers ->
                            total = headers.contentLength
                            flow.value = DownloadState.Running(0, total)
                        },
                        onProgress = { bytes -> flow.value = DownloadState.Running(bytes, total) },
                    )
                }
                val mimeType = metadata.mimeType
                val name = imageName(mimeType)
                val values = ContentValues().apply {
                    put(MediaStore.Downloads.DISPLAY_NAME, name)
                    put(MediaStore.Downloads.MIME_TYPE, mimeType)
                    put(MediaStore.Downloads.IS_PENDING, 1)
                }
                val inserted = context.contentResolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values)
                if (inserted == null) {
                    flow.value = DownloadState.Failed("Could not create the download file")
                    return@launch
                }
                uri = inserted
                val out = context.contentResolver.openOutputStream(inserted)
                    ?: error("Could not open the download file")
                out.use { destination ->
                    temp.inputStream().use { input -> input.copyTo(destination) }
                }
                context.contentResolver.update(
                    inserted,
                    ContentValues().apply { put(MediaStore.Downloads.IS_PENDING, 0) },
                    null,
                    null,
                )
                flow.value = DownloadState.Done(inserted, mimeType)
            } catch (e: Throwable) {
                uri?.let { context.contentResolver.delete(it, null, null) }
                flow.value = DownloadState.Failed(e.message ?: e.toString())
                if (e is kotlinx.coroutines.CancellationException) throw e
            } finally {
                temp.delete()
            }
        }
    }

    /** Opens a finished download with the matching app (APKs go to the installer). */
    fun open(done: DownloadState.Done): Boolean {
        val intent = Intent(Intent.ACTION_VIEW)
            .setDataAndType(done.uri, done.mimeType)
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        return try {
            context.startActivity(intent)
            true
        } catch (_: ActivityNotFoundException) {
            false
        }
    }

    companion object {
        fun mimeTypeFor(name: String): String =
            MimeTypeMap.getSingleton().getMimeTypeFromExtension(name.substringAfterLast('.', "").lowercase())
                ?: "application/octet-stream"

        private fun imageKey(url: String) = "image\u0000$url"

        private fun imageName(mimeType: String): String {
            val extension = MimeTypeMap.getSingleton().getExtensionFromMimeType(mimeType) ?: "bin"
            return "herdr-image-${System.currentTimeMillis()}.$extension"
        }
    }
}
