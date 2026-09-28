package com.tohutohu.herdrcompanion.data

import android.content.ContentResolver
import android.net.Uri
import android.provider.OpenableColumns
import com.tohutohu.herdrcompanion.data.api.GatewayApi
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

private const val MAX_UPLOAD_BYTES = 20 * 1024 * 1024

/** A file picked in the composer, held until the message is sent. */
data class Attachment(val uri: Uri, val name: String, val mime: String) {
    val isImage get() = mime.startsWith("image/")
}

/**
 * Reads the display name and type of a picked document. Touches the content
 * provider, so call it off the main thread.
 */
fun readAttachment(resolver: ContentResolver, uri: Uri): Attachment {
    val name = resolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { c ->
        if (c.moveToFirst() && !c.isNull(0)) c.getString(0) else null
    } ?: uri.lastPathSegment?.substringAfterLast('/') ?: "file"
    return Attachment(uri, name, resolver.getType(uri) ?: "application/octet-stream")
}

/** Uploads a picked file to the gateway and returns its upload id. */
suspend fun uploadAttachment(resolver: ContentResolver, api: GatewayApi, attachment: Attachment): String {
    val bytes = withContext(Dispatchers.IO) {
        resolver.openInputStream(attachment.uri)?.use { it.readBytes() }
            ?: error("cannot read ${attachment.name}")
    }
    // The gateway rejects anything larger, with a much vaguer message.
    if (bytes.size > MAX_UPLOAD_BYTES) {
        error("${attachment.name} is larger than ${MAX_UPLOAD_BYTES / (1024 * 1024)} MB")
    }
    return api.upload(bytes, attachment.mime, attachment.name)
}
