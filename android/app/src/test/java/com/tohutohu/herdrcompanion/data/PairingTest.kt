package com.tohutohu.herdrcompanion.data

import org.junit.Assert.*
import org.junit.Test

class PairingTest {
    private val code = "a".repeat(64)
    private fun qr(url: String) = "herdr-mobile://pair?url=${java.net.URLEncoder.encode(url, "UTF-8")}#$code"

    @Test fun `MacのQRからTailscale接続先と一時コードを読む`() {
        val invitation = PairingInvitation.parse(qr("http://100.99.15.34:8766"))
        assertEquals("100.99.15.34", invitation.gateway.host)
        assertEquals(8766, invitation.gateway.port)
        assertEquals(code, invitation.code)
    }

    @Test fun `公開ホストやユーザー情報や追加パスを受け入れない`() {
        listOf("http://evil.example", "http://100.99.15.34.evil.example", "http://127.0.0.1:8766", "http://100.128.0.1",
            "http://user:password@100.99.15.34", "http://100.99.15.34/pair", "http://100.99.15.34?other=1").forEach {
            assertThrows(IllegalArgumentException::class.java) { PairingInvitation.parse(qr(it)) }
        }
    }

    @Test fun `偽のスキームや短いコードを受け入れない`() {
        val valid = qr("http://100.99.15.34:8766")
        listOf(valid.replace("herdr-mobile:", "https:"), valid.replace(code, "123456"), valid.replace("?url=", "?other=x&url=")).forEach {
            assertThrows(IllegalArgumentException::class.java) { PairingInvitation.parse(it) }
        }
    }

    @Test fun `Firebaseなしの接続設定を保存できる`() {
        val settings = Settings("http://100.99.15.34:8766", code, connectionId = "device")
        val json = com.tohutohu.herdrcompanion.data.api.GatewayApi.json
        assertEquals(settings, json.decodeFromString<Settings>(json.encodeToString(settings)))
        assertNull(settings.firebase)
        assertTrue(settings.isConfigured)
    }
}
