package com.tohutohu.herdrcompanion.dictate

import android.content.ContentProvider
import android.content.ContentValues
import android.database.Cursor
import android.net.Uri
import android.os.Bundle
import com.tohutohu.herdrcompanion.container

/**
 * Exported so Dictate can call it; only Dictate's packages holding the token
 * of a field that is still alive get anything but "denied".
 */
class DictateContextProvider : ContentProvider() {
    override fun call(method: String, arg: String?, extras: Bundle?): Bundle? {
        val context = context ?: return null
        val callerAllowed = callingPackage in DictateContract.ALLOWED_CALLERS
        val reply = context.container.dictateContexts
            .handle(method, extras?.getString(DictateContract.KEY_PAYLOAD_JSON), callerAllowed)
            ?: return null
        return Bundle().apply {
            putString(DictateContract.KEY_ACTION, reply.action)
            putString(DictateContract.KEY_PAYLOAD_JSON, reply.payloadJson)
        }
    }

    override fun onCreate() = true
    override fun query(uri: Uri, projection: Array<out String>?, selection: String?, selectionArgs: Array<out String>?, sortOrder: String?): Cursor? = null
    override fun getType(uri: Uri): String? = null
    override fun insert(uri: Uri, values: ContentValues?): Uri? = null
    override fun delete(uri: Uri, selection: String?, selectionArgs: Array<out String>?) = 0
    override fun update(uri: Uri, values: ContentValues?, selection: String?, selectionArgs: Array<out String>?) = 0
}
