package com.tohutohu.herdrcompanion.ui.forward

import com.tohutohu.herdrcompanion.data.api.LocalPortDto
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/** Hosts that name the Mac itself in a URL an agent prints. 0.0.0.0 is what some servers log. */
private val LOCAL_HOSTS = setOf("localhost", "127.0.0.1", "[::1]", "::1", "0.0.0.0")

/**
 * The URL to open through the Gateway when [url] points at the Mac's own localhost, or null for
 * any other link. 0.0.0.0 becomes localhost, which is what a browser on the Mac would reach.
 */
internal fun localhostTarget(url: String): String? {
    val parsed = url.trim().toHttpUrlOrNull() ?: return null
    if (parsed.host.lowercase() !in LOCAL_HOSTS && !parsed.host.startsWith("127.")) return null
    return if (parsed.host == "0.0.0.0") parsed.newBuilder().host("localhost").build().toString() else parsed.toString()
}

/** The proxy address for the Gateway at [gatewayUrl]: only a plain-HTTP Gateway can be one. */
internal fun proxyRuleFor(gatewayUrl: String): String? {
    val parsed = gatewayUrl.trim().toHttpUrlOrNull() ?: return null
    if (parsed.scheme != "http") return null
    return "http://${parsed.host}:${parsed.port}"
}

/** Servers started inside [sessionCwd] first, then the rest, each by port. */
internal fun sessionPortsFirst(ports: List<LocalPortDto>, sessionCwd: String?): Pair<List<LocalPortDto>, List<LocalPortDto>> {
    val root = sessionCwd?.trimEnd('/')?.takeIf { it.isNotEmpty() }
        ?: return emptyList<LocalPortDto>() to ports.sortedBy { it.port }
    val (inSession, others) = ports.sortedBy { it.port }.partition { p ->
        val cwd = p.cwd ?: return@partition false
        cwd == root || cwd.startsWith("$root/")
    }
    return inSession to others
}
