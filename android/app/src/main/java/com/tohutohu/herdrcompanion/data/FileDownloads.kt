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
            val uri = try {
                insertDownload(name, mimeType)
            } catch (e: Exception) {
                flow.value = DownloadState.Failed(e.message ?: e.toString())
                return@launch
            }
            if (uri == null) {
                flow.value = DownloadState.Failed("Could not create the download file")
                return@launch
            }
            try {
                val out = resolver.openOutputStream(uri) ?: error("Could not open the download file")
                out.use { api.downloadFile(sessionId, path, it) { n -> flow.value = DownloadState.Running(n, size) } }
                publish(uri, name)
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
                val inserted = insertDownload(name, mimeType)
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
                publish(inserted, name)
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

    private fun insertDownload(name: String, mimeType: String): Uri? = context.contentResolver.insert(
        MediaStore.Downloads.EXTERNAL_CONTENT_URI,
        ContentValues().apply {
            put(MediaStore.Downloads.DISPLAY_NAME, name)
            put(MediaStore.Downloads.MIME_TYPE, mimeType)
            put(MediaStore.Downloads.IS_PENDING, 1)
        },
    )

    /**
     * Makes a finished download visible under [name]. MediaStore numbers a
     * taken name ("app-release (1).apk", …) only up to a limit and then
     * refuses with "Failed to build unique file", so after many downloads of
     * the same file the name gets the time instead.
     */
    private fun publish(uri: Uri, name: String) {
        val resolver = context.contentResolver
        try {
            resolver.update(uri, ContentValues().apply { put(MediaStore.Downloads.IS_PENDING, 0) }, null, null)
        } catch (_: IllegalStateException) {
            resolver.update(
                uri,
                ContentValues().apply {
                    put(MediaStore.Downloads.DISPLAY_NAME, timestampedName(name, System.currentTimeMillis()))
                    put(MediaStore.Downloads.IS_PENDING, 0)
                },
                null,
                null,
            )
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

        /** "app-release.apk" → "app-release-20261030-153212.apk" (local time). */
        fun timestampedName(name: String, epochMillis: Long): String {
            val stamp = java.text.SimpleDateFormat("yyyyMMdd-HHmmss", java.util.Locale.ROOT).format(java.util.Date(epochMillis))
            val dot = name.lastIndexOf('.')
            return if (dot > 0) name.substring(0, dot) + "-" + stamp + name.substring(dot) else "$name-$stamp"
        }

        private fun imageKey(url: String) = "image\u0000$url"

        private fun imageName(mimeType: String): String {
            val extension = MimeTypeMap.getSingleton().getExtensionFromMimeType(mimeType) ?: "bin"
            return "herdr-image-${System.currentTimeMillis()}.$extension"
        }
    }
}
