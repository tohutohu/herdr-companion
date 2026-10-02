package com.tohutohu.herdrcompanion.desktop

import com.tohutohu.herdrcompanion.data.Settings
import com.tohutohu.herdrcompanion.data.api.GatewayApi
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.net.URI
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.Paths
import java.nio.file.StandardCopyOption
import java.nio.file.attribute.PosixFilePermissions

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
    /** True when the address and token were entered in Settings rather than read from the manager's config. */
    val manual: Boolean = false,
) {
    val settings: Settings get() = Settings(gatewayUrl = baseUrl, token = token)
}

enum class DesktopConnectionState {
    CONNECTING,
    CONNECTED,
    RECONNECTING,
    OFFLINE,
    AUTHENTICATION_FAILED,
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
        val explicitUrl = properties["herdr.gateway.url"]?.trim()?.takeIf { it.isNotEmpty() }
            ?: environment["HERDR_DESKTOP_GATEWAY_URL"]?.trim()?.takeIf { it.isNotEmpty() }
        DesktopManualConnectionStore.load(home)?.let { manual ->
            return DesktopGatewayConnection(
                baseUrl = explicitUrl ?: manual.gatewayUrl,
                token = manual.token,
                configPath = DesktopManualConnectionStore.path(home),
                manual = true,
            )
        }

        val desktopPath = home.resolve(".config/herdr-mobile/desktop/config.json")
        val legacyPath = home.resolve(".config/herdr-mobile/config.json")
        val selected = when {
            Files.isRegularFile(desktopPath) -> desktopPath to DESKTOP_PORT
            Files.isRegularFile(legacyPath) -> legacyPath to LEGACY_PORT
            else -> null
        }
        val defaultPort = selected?.second ?: DESKTOP_PORT

        if (selected == null) {
            return DesktopGatewayConnection(
                baseUrl = explicitUrl ?: "http://127.0.0.1:$defaultPort",
                token = "",
                configPath = null,
                configIssue = "Gateway config not found. Start Herdr Companion Gateway, or enter the Gateway address and token in Settings.",
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

/** A Gateway address and token entered in Settings, for machines without the menu-bar manager (e.g. Windows). */
@Serializable
data class DesktopManualConnection(val gatewayUrl: String, val token: String)

/**
 * Keeps the manual connection in its own file so the Gateway manager's config
 * is never rewritten by the UI. It holds the auth token, so it is owner-only
 * where the filesystem supports POSIX permissions.
 */
object DesktopManualConnectionStore {
    private val json = Json { ignoreUnknownKeys = true }

    fun path(home: Path): Path = home.resolve(".config/herdr-mobile/desktop-ui/connection.json")

    fun load(home: Path): DesktopManualConnection? {
        val text = runCatching { Files.readString(path(home)) }.getOrNull() ?: return null
        val stored = runCatching { json.decodeFromString<DesktopManualConnection>(text) }.getOrNull() ?: return null
        return validateManualConnection(stored.gatewayUrl, stored.token).getOrNull()
    }

    fun save(home: Path, connection: DesktopManualConnection) {
        val target = path(home)
        Files.createDirectories(target.parent)
        val temp = Files.createTempFile(target.parent, "connection", ".tmp")
        try {
            runCatching { Files.setPosixFilePermissions(temp, PosixFilePermissions.fromString("rw-------")) }
            Files.writeString(temp, json.encodeToString(connection))
            Files.move(temp, target, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE)
        } finally {
            Files.deleteIfExists(temp)
        }
    }

    fun clear(home: Path) {
        Files.deleteIfExists(path(home))
    }
}

/** Normalizes what the user typed into a connection, or explains what is wrong with it. */
internal fun validateManualConnection(gatewayUrl: String, token: String): Result<DesktopManualConnection> = runCatching {
    val trimmedUrl = gatewayUrl.trim().trimEnd('/')
    val uri = runCatching { URI(trimmedUrl) }.getOrNull()
    require(
        uri != null && (uri.scheme == "http" || uri.scheme == "https") && !uri.host.isNullOrEmpty() &&
            uri.rawUserInfo == null && uri.rawPath.isNullOrEmpty() && uri.rawQuery == null && uri.rawFragment == null,
    ) { "Enter the Gateway address as http://host:port or https://host." }
    val trimmedToken = token.trim()
    require(trimmedToken.isNotEmpty()) { "Enter the Gateway auth token." }
    require(trimmedToken.none(Char::isWhitespace)) { "The auth token cannot contain spaces." }
    DesktopManualConnection(trimmedUrl, trimmedToken)
}
