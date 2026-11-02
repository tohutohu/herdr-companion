package com.tohutohu.herdrcompanion.ui.forward

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
