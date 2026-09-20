package com.tohutohu.herdrcompanion.desktop

import com.tohutohu.herdrcompanion.data.api.GatewayException
import java.io.IOException
import kotlin.test.Test
import kotlin.test.assertEquals

class DesktopErrorsTest {
    @Test
    fun `authentication failures are user-facing and do not expose response details`() {
        val error = GatewayException(401, "Bearer top-secret").toDesktopGatewayError()

        assertEquals(DesktopGatewayErrorKind.AUTHENTICATION, error.kind)
        assertEquals("Authentication error. Check the Desktop Gateway config token.", error.message)
    }

    @Test
    fun `stream failures keep a retryable streaming state`() {
        val error = IOException("connection reset").toDesktopGatewayError(streaming = true)

        assertEquals(DesktopGatewayErrorKind.STREAMING_DISCONNECTED, error.kind)
        assertEquals("Streaming disconnected. Retrying…", error.message)
        kotlin.test.assertEquals(true, error.retryable)
    }
}
