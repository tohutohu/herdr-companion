package com.tohutohu.herdrcompanion.data

/**
 * `hc://session/<session id>`: a link that opens one session in the Android
 * app or the desktop UI. It is not `herdr-companion://`, which the Mac
 * Gateway Manager already owns for `gateway/start` and the pairing QR.
 */
object SessionLink {
    const val SCHEME = "hc"
    const val HOST = "session"
    private const val PREFIX = "$SCHEME://$HOST/"
    private const val MAX_ID_LENGTH = 200

    fun url(sessionId: String): String = PREFIX + sessionId.encodeToByteArray().joinToString("") { byte ->
        val char = byte.toInt().toChar()
        if (byte >= 0 && (char.isLetterOrDigit() || char in "-._~:")) char.toString()
        else "%" + (byte.toInt() and 0xff).toString(16).uppercase().padStart(2, '0')
    }

    /** The session a link names, or null when it is not a session link. */
    fun sessionId(link: String): String? {
        if (!link.startsWith(PREFIX, ignoreCase = true)) return null
        val raw = link.substring(PREFIX.length).substringBefore('?').substringBefore('#').trimEnd('/')
        val id = percentDecode(raw) ?: return null
        if (id.isBlank() || id.length > MAX_ID_LENGTH || id.any { it.isISOControl() || it == '/' }) return null
        return id
    }

    private fun percentDecode(value: String): String? {
        val bytes = ArrayList<Byte>(value.length)
        var i = 0
        while (i < value.length) {
            val char = value[i]
            if (char == '%') {
                val byte = value.substring(i + 1, minOf(i + 3, value.length)).takeIf { it.length == 2 }?.toIntOrNull(16) ?: return null
                bytes += byte.toByte()
                i += 3
            } else {
                char.toString().encodeToByteArray().forEach { bytes += it }
                i++
            }
        }
        return runCatching { bytes.toByteArray().decodeToString(throwOnInvalidSequence = true) }.getOrNull()
    }
}
