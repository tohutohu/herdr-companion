package com.tohutohu.herdrcompanion.ui.detail

import com.tohutohu.herdrcompanion.data.api.GatewayException
import java.io.IOException
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.fail
import org.junit.Test

class RefreshErrorTest {
    @Test
    fun 取得に成功したらエラーを消す() = runBlocking {
        assertNull(refreshError {})
    }

    @Test
    fun 通信失敗はオフラインとして表示する() = runBlocking {
        assertEquals(
            "Offline: showing cached messages (timeout)",
            refreshError { throw IOException("timeout") },
        )
    }

    @Test
    fun ゲートウェイのエラーはそのまま表示する() = runBlocking {
        assertEquals("session not found", refreshError { throw GatewayException(404, "session not found") })
    }

    @Test
    fun 画面を離れてキャンセルされた取得はオフライン扱いにしない() = runBlocking {
        try {
            refreshError { throw CancellationException("StandaloneCoroutine was cancelled") }
            fail("cancellation must propagate")
        } catch (_: CancellationException) {
        }
    }
}
