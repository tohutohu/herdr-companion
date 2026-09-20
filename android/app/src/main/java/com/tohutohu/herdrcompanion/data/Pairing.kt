package com.tohutohu.herdrcompanion.data

import com.tohutohu.herdrcompanion.data.api.GatewayApi
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import java.net.URI
import java.net.URLDecoder
import java.util.UUID
import java.util.concurrent.TimeUnit

data class PairingInvitation(val gateway: HttpUrl, val code: String) {
    companion object {
        fun parse(raw: String): PairingInvitation {
            require(raw.length <= 2048) { "Invalid pairing QR" }
            val uri = URI(raw.trim())
            require(uri.scheme == "herdr-mobile" && uri.host == "pair" && uri.rawUserInfo == null && uri.port == -1 && uri.path.isNullOrEmpty()) { "Scan a Herdr Mobile pairing QR" }
            val params = uri.rawQuery.orEmpty().split("&")
            require(params.size == 1 && params[0].startsWith("url=")) { "Invalid pairing URL" }
            val gateway = URLDecoder.decode(params[0].substring(4), "UTF-8").toHttpUrl()
            require(gateway.username.isEmpty() && gateway.password.isEmpty() && gateway.encodedPath == "/" && gateway.query == null && gateway.fragment == null) { "Invalid gateway address" }
            // The desktop app pairs exclusively across the encrypted tailnet.
            val octets = gateway.host.split('.').mapNotNull { it.toIntOrNull() }
            require(gateway.scheme == "http" && octets.size == 4 && gateway.host == octets.joinToString(".") && octets[0] == 100 && octets[1] in 64..127 && octets.all { it in 0..255 }) { "Pairing requires a Tailscale IPv4 address" }
            val code = uri.fragment.orEmpty()
            require(code.matches(Regex("[0-9a-f]{64}"))) { "Invalid pairing code" }
            return PairingInvitation(gateway, code)
        }
    }
}

@Serializable private data class PairingRequest(val code: String)
@Serializable private data class PairingResponse(val token: String, val gatewayId: String, val firebase: FirebaseSettings? = null)

class PairingClient {
    // Never follow redirects carrying a one-use invitation to a different endpoint.
    private val http = OkHttpClient.Builder().followRedirects(false).followSslRedirects(false)
        .callTimeout(15, TimeUnit.SECONDS).build()

    suspend fun redeem(invitation: PairingInvitation): Settings = withContext(Dispatchers.IO) {
        val request = Request.Builder().url(invitation.gateway.newBuilder().addPathSegment("pair").build())
            .post(GatewayApi.json.encodeToString(PairingRequest(invitation.code)).toRequestBody("application/json".toMediaType())).build()
        http.newCall(request).execute().use { response ->
            require(response.isSuccessful) { "Pairing failed (${response.code}). Generate a new QR on the Mac." }
            val source = response.body.source()
            require(!source.request(16_385)) { "Invalid pairing response" }
            val paired = GatewayApi.json.decodeFromString<PairingResponse>(source.readUtf8())
            require(paired.token.matches(Regex("[0-9a-f]{64}"))) { "Invalid gateway token" }
            require(paired.gatewayId.matches(Regex("[0-9a-f]{64}"))) { "Invalid gateway identity" }
            paired.firebase?.let {
                require(it.projectId.isNotBlank() && it.apiKey.isNotBlank() && it.senderId.matches(Regex("[0-9]+")) && it.applicationId.startsWith("1:${it.senderId}:android:")) { "Invalid Firebase configuration" }
            }
            Settings(invitation.gateway.toString().trimEnd('/'), paired.token, paired.firebase, UUID.randomUUID().toString(), paired.gatewayId)
        }
    }
}
