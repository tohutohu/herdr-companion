package com.tohutohu.herdrcompanion.data

import android.content.ContentResolver
import android.net.Uri
import android.provider.OpenableColumns
import com.tohutohu.herdrcompanion.data.api.GatewayApi
import java.io.IOException

/** A file picked in the composer, held until the message is sent. */
data class Attachment(val uri: Uri, val name: String, val mime: String, val size: Long? = null) {
    val isImage get() = mime.startsWith("image/")
}

/**
 * Reads the display name and type of a picked document. Touches the content
 * provider, so call it off the main thread.
 */
fun readAttachment(resolver: ContentResolver, uri: Uri): Attachment {
    var size: Long? = null
    val name = resolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)?.use { c ->
        if (!c.moveToFirst()) return@use null
        val sizeColumn = c.getColumnIndex(OpenableColumns.SIZE)
        if (sizeColumn >= 0 && !c.isNull(sizeColumn)) size = c.getLong(sizeColumn).takeIf { it >= 0 }
        val nameColumn = c.getColumnIndex(OpenableColumns.DISPLAY_NAME)
        if (nameColumn >= 0 && !c.isNull(nameColumn)) c.getString(nameColumn) else null
    } ?: uri.lastPathSegment?.substringAfterLast('/') ?: "file"
    return Attachment(uri, name, resolver.getType(uri) ?: "application/octet-stream", size)
}

/** Uploads a picked file to the gateway and returns its upload id. */
suspend fun uploadAttachment(resolver: ContentResolver, api: GatewayApi, attachment: Attachment): String {
    return api.upload(attachment.mime, attachment.name, attachment.size) {
        resolver.openInputStream(attachment.uri) ?: throw IOException("cannot read ${attachment.name}")
    }
}
