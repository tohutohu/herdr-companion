package com.tohutohu.herdrcompanion.data

import org.junit.Assert.*
import org.junit.Test

class PairingTest {
    private val code = "a".repeat(64)
    private fun qr(url: String) = "herdr-companion://pair?url=${java.net.URLEncoder.encode(url, "UTF-8")}#$code"

    @Test fun `MacのQRからTailscale接続先と一時コードを読む`() {
        val invitation = PairingInvitation.parse(qr("http://100.99.15.34:8766"))
        assertEquals("100.99.15.34", invitation.gateway.host)
        assertEquals(8766, invitation.gateway.port)
        assertEquals(code, invitation.code)
    }

    @Test fun `ローカルネットワークのQRも受け入れる`() {
        listOf("http://192.168.1.23:8766", "http://10.0.0.8:8766", "http://172.16.2.4:8766", "http://my-mac.local:8766").forEach {
            assertEquals(it.substringAfter("//").substringBefore(':'), PairingInvitation.parse(qr(it)).gateway.host)
        }
    }

    @Test fun `公開ホストやユーザー情報や追加パスを受け入れない`() {
        listOf("http://evil.example", "http://100.99.15.34.evil.example", "http://127.0.0.1:8766", "http://8.8.8.8:8766", "http://100.128.0.1",
            "http://user:password@100.99.15.34", "http://100.99.15.34/pair", "http://100.99.15.34?other=1").forEach {
            assertThrows(IllegalArgumentException::class.java) { PairingInvitation.parse(qr(it)) }
        }
    }

    @Test fun `偽のスキームや短いコードを受け入れない`() {
        val valid = qr("http://100.99.15.34:8766")
        listOf(valid.replace("herdr-companion:", "https:"), valid.replace(code, "123456"), valid.replace("?url=", "?other=x&url=")).forEach {
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
