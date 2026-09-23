package com.tohutohu.herdrcompanion.data

import org.junit.Assert.*
import org.junit.Test

class ManualConnectionTest {
    private val firebase = FirebaseSettings("key", "1:123:android:abc", "project", "123")
    private val paired = Settings("http://100.99.15.34:8765", "a".repeat(64), firebase, "conn", "b".repeat(64))

    @Test fun `URLもトークンも変えなければ今の接続をそのまま使う`() {
        assertSame(paired, manualConnection(paired, " http://100.99.15.34:8765 ", "a".repeat(64)))
    }

    @Test fun `同じトークンでURLだけ変えてもFirebase設定とGateway IDを引き継ぐ`() {
        val next = manualConnection(paired, "https://herdr.example.com", "a".repeat(64))
        assertEquals("https://herdr.example.com", next.gatewayUrl)
        assertEquals(firebase, next.firebase)
        assertEquals(paired.gatewayId, next.gatewayId)
        assertNotEquals(paired.connectionId, next.connectionId)
    }

    @Test fun `トークンを変えると別のGatewayとしてFirebase設定を持ち越さない`() {
        val next = manualConnection(paired, "http://100.99.15.34:8765", "c".repeat(64))
        assertNull(next.firebase)
        assertEquals("", next.gatewayId)
        assertTrue(next.connectionId.isNotEmpty())
    }
}
