package com.tohutohu.herdrmobile.desktop

import com.tohutohu.herdrmobile.data.Settings
import com.tohutohu.herdrmobile.data.api.GatewayApi
import kotlinx.serialization.Serializable
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.Paths

/** The fields the existing macOS menu-bar manager writes to its config. */
@Serializable
private data class GatewayConfigFile(
    val authToken: String = "",
    /** Present in the actual Gateway config when a non-default listen address was used. */
    val listen: String? = null,
    val gatewayUrl: String? = null,
    val url: String? = null,
    val host: String? = null,
    val port: Int? = null,
)

data class DesktopGatewayConnection(
    val baseUrl: String,
    val token: String,
    val configPath: Path?,
    val configIssue: String? = null,
) {
    val settings: Settings get() = Settings(gatewayUrl = baseUrl, token = token)
}

/** Resolves Desktop's local connection without duplicating the Android pairing flow. */
object DesktopConnectionConfig {
    const val DESKTOP_PORT = 8766
    const val LEGACY_PORT = 8765

    private val json = GatewayApi.json

    fun load(
        home: Path = Paths.get(System.getProperty("user.home") ?: "."),
        environment: Map<String, String> = System.getenv(),
        properties: Map<String, String> = System.getProperties().stringPropertyNames().associateWith { System.getProperty(it).orEmpty() },
    ): DesktopGatewayConnection {
        val desktopPath = home.resolve(".config/herdr-mobile/desktop/config.json")
        val legacyPath = home.resolve(".config/herdr-mobile/config.json")
        val selected = when {
            Files.isRegularFile(desktopPath) -> desktopPath to DESKTOP_PORT
            Files.isRegularFile(legacyPath) -> legacyPath to LEGACY_PORT
            else -> null
        }
        val explicitUrl = properties["herdr.gateway.url"]?.trim()?.takeIf { it.isNotEmpty() }
            ?: environment["HERDR_DESKTOP_GATEWAY_URL"]?.trim()?.takeIf { it.isNotEmpty() }
        val defaultPort = selected?.second ?: DESKTOP_PORT

        if (selected == null) {
            return DesktopGatewayConnection(
                baseUrl = explicitUrl ?: "http://127.0.0.1:$defaultPort",
                token = "",
                configPath = null,
                configIssue = "Gateway config not found. Start the macOS Gateway manager first.",
            )
        }

        val (path, port) = selected
        val parsed = runCatching { json.decodeFromString<GatewayConfigFile>(Files.readString(path)) }
        val config = parsed.getOrNull()
        val issue = parsed.exceptionOrNull()?.let { "Gateway config could not be read." }
        val configuredUrl = config?.gatewayUrl?.trim()?.takeIf { it.isNotEmpty() }
            ?: config?.url?.trim()?.takeIf { it.isNotEmpty() }
            ?: config?.host?.trim()?.takeIf { it.isNotEmpty() }?.let { host ->
                "http://$host:${config.port ?: port}"
            }
            ?: config?.listen?.let(::configuredListenUrl)
        return DesktopGatewayConnection(
            baseUrl = explicitUrl ?: configuredUrl ?: "http://127.0.0.1:${config?.port ?: port}",
            token = config?.authToken?.trim().orEmpty(),
            configPath = path,
            configIssue = issue ?: if (config?.authToken.isNullOrBlank()) {
                "Gateway auth token is missing from the Desktop config."
            } else {
                null
            },
        )
    }

    /** Small pure parser used by tests and by future settings UI. */
    internal fun parseConfig(jsonText: String, defaultUrl: String): DesktopGatewayConnection {
        val parsed = runCatching { json.decodeFromString<GatewayConfigFile>(jsonText) }
        val config = parsed.getOrNull()
        return DesktopGatewayConnection(
            baseUrl = config?.gatewayUrl?.trim()?.takeIf { it.isNotEmpty() }
                ?: config?.listen?.let(::configuredListenUrl)
                ?: defaultUrl,
            token = config?.authToken?.trim().orEmpty(),
            configPath = null,
            configIssue = when {
                parsed.isFailure -> "Gateway config could not be read."
                config?.authToken.isNullOrBlank() -> "Gateway auth token is missing from the Desktop config."
                else -> null
            },
        )
    }

    /** `:8765` is the Go config default; the menu-bar manager overrides it with 8766. */
    private fun configuredListenUrl(listen: String): String? {
        val value = listen.trim().removePrefix("http://").removePrefix("https://")
        if (value.isEmpty() || value.startsWith(":")) return null
        return "http://$value"
    }
}
