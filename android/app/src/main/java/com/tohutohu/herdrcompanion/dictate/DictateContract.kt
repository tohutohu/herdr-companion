package com.tohutohu.herdrcompanion.dictate

import android.content.Context
import android.net.Uri
import android.os.Bundle
import android.util.Base64
import android.view.inputmethod.EditorInfo
import com.tohutohu.herdrcompanion.BuildConfig
import java.security.SecureRandom
import java.util.concurrent.ConcurrentHashMap

/**
 * Names from Dictate's input-context protocol v1: Dictate reads the
 * declaration in a field's EditorInfo, then calls [DictateContextProvider]
 * for the project's vocabulary and replacement rules.
 */
object DictateContract {
    const val BASE = "net.devemperor.dictate.imecontext.v1"
    const val VERSION = 1
    const val EXTRA_SUPPORTED_VERSION = "$BASE.SUPPORTED_VERSION"
    const val EXTRA_PROVIDER_AUTHORITY = "$BASE.PROVIDER_AUTHORITY"
    const val EXTRA_SESSION_TOKEN = "$BASE.SESSION_TOKEN"
    const val KEY_PAYLOAD_JSON = "$BASE.PAYLOAD_JSON"
    const val KEY_ACTION = "$BASE.ACTION"
    const val GET_CONTEXT = "$BASE.GET_CONTEXT"
    const val CONTEXT_RESULT = "$BASE.CONTEXT_RESULT"
    const val ADD_VOCABULARY = "$BASE.ADD_VOCABULARY"
    const val ADD_REPLACEMENT = "$BASE.ADD_REPLACEMENT"
    const val MUTATION_RESULT = "$BASE.MUTATION_RESULT"

    /** Must match the provider's authority in AndroidManifest.xml. */
    const val AUTHORITY = "${BuildConfig.APPLICATION_ID}.dictatecontext"
    val ALLOWED_CALLERS = setOf("net.devemperor.dictate", "net.devemperor.dictate.debug", "net.devemperor.dictate.beta")

    fun declare(outAttrs: EditorInfo, token: String) {
        val extras = outAttrs.extras ?: Bundle().also { outAttrs.extras = it }
        extras.putInt(EXTRA_SUPPORTED_VERSION, VERSION)
        extras.putString(EXTRA_PROVIDER_AUTHORITY, AUTHORITY)
        extras.putString(EXTRA_SESSION_TOKEN, token)
    }

    fun newToken(): String = ByteArray(16).also { SecureRandom().nextBytes(it) }
        .let { Base64.encodeToString(it, Base64.URL_SAFE or Base64.NO_WRAP or Base64.NO_PADDING) }

    fun notifyInvalidated(context: Context) {
        context.contentResolver.notifyChange(Uri.parse("content://$AUTHORITY/CONTEXT_INVALIDATED"), null)
    }
}

/** Live input fields by session token, each with the project it currently points at. */
class DictateFieldRegistry {
    private val fields = ConcurrentHashMap<String, () -> String?>()

    fun register(token: String, project: () -> String?) { fields[token] = project }
    fun unregister(token: String) { fields.remove(token) }
    fun isLive(token: String?): Boolean = token != null && fields.containsKey(token)
    fun projectFor(token: String?): String? = token?.let { fields[it] }?.invoke()?.takeIf { it.isNotBlank() }
}
