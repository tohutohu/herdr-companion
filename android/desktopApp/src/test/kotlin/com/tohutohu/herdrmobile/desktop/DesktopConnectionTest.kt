package com.tohutohu.herdrmobile.desktop

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class DesktopConnectionTest {
    @Test
    fun `macOS manager config supplies token and local default port`() {
        val connection = DesktopConnectionConfig.parseConfig(
            """{"authToken":"secret"}""",
            "http://127.0.0.1:8766",
        )

        assertEquals("http://127.0.0.1:8766", connection.baseUrl)
        assertEquals("secret", connection.token)
        assertNull(connection.configIssue)
    }

    @Test
    fun `explicit gateway URL in config wins over default`() {
        val connection = DesktopConnectionConfig.parseConfig(
            """{"authToken":"secret","gatewayUrl":"http://localhost:9999"}""",
            "http://127.0.0.1:8766",
        )

        assertEquals("http://localhost:9999", connection.baseUrl)
    }

    @Test
    fun `a configured non-default listen address is honored`() {
        val connection = DesktopConnectionConfig.parseConfig(
            """{"authToken":"secret","listen":"100.99.15.34:8765"}""",
            "http://127.0.0.1:8766",
        )

        assertEquals("http://100.99.15.34:8765", connection.baseUrl)
    }
}
