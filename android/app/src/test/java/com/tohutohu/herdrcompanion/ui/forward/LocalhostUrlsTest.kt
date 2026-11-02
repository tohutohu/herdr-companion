package com.tohutohu.herdrcompanion.ui.forward

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class LocalhostUrlsTest {
    @Test
    fun `Macのlocalhostを指すURLだけを転送先にする`() {
        assertEquals("http://localhost:5173/", localhostTarget("http://localhost:5173"))
        assertEquals("http://127.0.0.1:8000/api?x=1", localhostTarget("http://127.0.0.1:8000/api?x=1"))
        assertEquals("http://[::1]:3000/", localhostTarget("http://[::1]:3000/"))
        assertEquals("https://localhost:8443/", localhostTarget("https://localhost:8443/"))
        assertNull(localhostTarget("https://example.com/"))
        assertNull(localhostTarget("http://100.99.15.34:8765/"))
        assertNull(localhostTarget("http://localhost.example.com/"))
        assertNull(localhostTarget("mailto:a@example.com"))
    }

    @Test
    fun `0_0_0_0はlocalhostとして開く`() {
        assertEquals("http://localhost:8000/docs", localhostTarget("http://0.0.0.0:8000/docs"))
    }

    @Test
    fun `平文HTTPのGatewayだけをプロキシにできる`() {
        assertEquals("http://100.99.15.34:8765", proxyRuleFor("http://100.99.15.34:8765/"))
        assertEquals("http://10.0.2.2:80", proxyRuleFor("http://10.0.2.2"))
        assertNull(proxyRuleFor("https://tunnel.example.com"))
        assertNull(proxyRuleFor(""))
    }

}
