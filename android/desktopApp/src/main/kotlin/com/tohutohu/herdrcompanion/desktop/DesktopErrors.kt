package com.tohutohu.herdrcompanion.desktop

import com.tohutohu.herdrcompanion.data.api.GatewayException
import java.io.IOException

enum class DesktopGatewayErrorKind {
    GATEWAY_OFFLINE,
    AUTHENTICATION,
    NETWORK,
    API,
    STREAMING_DISCONNECTED,
    SESSION_NOT_FOUND,
    UNKNOWN,
}

data class DesktopGatewayError(
    val kind: DesktopGatewayErrorKind,
    val message: String,
    val retryable: Boolean,
)

fun Throwable.toDesktopGatewayError(streaming: Boolean = false): DesktopGatewayError {
    if (this is GatewayException) {
        return when {
            code == 401 || code == 403 -> DesktopGatewayError(
                DesktopGatewayErrorKind.AUTHENTICATION,
                "Authentication error. Check the Desktop Gateway config token.",
                retryable = false,
            )
            code == 404 -> DesktopGatewayError(
                DesktopGatewayErrorKind.SESSION_NOT_FOUND,
                "Session not found.",
                retryable = false,
            )
            code == 0 -> DesktopGatewayError(
                DesktopGatewayErrorKind.GATEWAY_OFFLINE,
                "Gateway configuration is unavailable.",
                retryable = true,
            )
            streaming -> DesktopGatewayError(
                DesktopGatewayErrorKind.STREAMING_DISCONNECTED,
                "Streaming disconnected. Retrying…",
                retryable = true,
            )
            else -> DesktopGatewayError(
                DesktopGatewayErrorKind.API,
                "Gateway API error: ${safeMessage(message)}",
                retryable = code >= 500,
            )
        }
    }
    if (this is IOException) {
        return DesktopGatewayError(
            if (streaming) DesktopGatewayErrorKind.STREAMING_DISCONNECTED else DesktopGatewayErrorKind.NETWORK,
            if (streaming) "Streaming disconnected. Retrying…" else "Gateway unavailable. Check that the local Gateway is running.",
            retryable = true,
        )
    }
    return DesktopGatewayError(
        DesktopGatewayErrorKind.UNKNOWN,
        "Unexpected Gateway error. Retry the operation.",
        retryable = true,
    )
}

private fun safeMessage(message: String?): String {
    val clean = message.orEmpty().replace(Regex("(?i)bearer\\s+\\S+"), "Bearer [redacted]").trim()
    return clean.take(240).ifEmpty { "request failed" }
}
