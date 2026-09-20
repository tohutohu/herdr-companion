package com.tohutohu.herdrcompanion.data

import kotlinx.serialization.Serializable

/** Platform-neutral gateway settings used by the shared JVM API client. */
@Serializable
data class FirebaseSettings(
    val apiKey: String,
    val applicationId: String,
    val projectId: String,
    val senderId: String,
)

@Serializable
data class Settings(
    val gatewayUrl: String = "",
    val token: String = "",
    val firebase: FirebaseSettings? = null,
    val connectionId: String = "",
    val gatewayId: String = "",
) {
    val isConfigured get() = gatewayUrl.isNotBlank() && token.isNotBlank()
}
