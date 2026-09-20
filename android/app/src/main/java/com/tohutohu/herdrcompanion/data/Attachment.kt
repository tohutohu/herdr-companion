package com.tohutohu.herdrcompanion.data

import android.content.ContentResolver
import android.net.Uri
import android.provider.OpenableColumns

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
