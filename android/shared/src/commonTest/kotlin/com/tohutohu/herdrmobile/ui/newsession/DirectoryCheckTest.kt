package com.tohutohu.herdrmobile.ui.newsession

import com.tohutohu.herdrmobile.data.api.DirectoryCheckResult
import com.tohutohu.herdrmobile.data.api.StartSessionRequest
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.runBlocking
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.fail

class DirectoryCheckTest {
    private val request = StartSessionRequest("claude", "/workspace/app", "通知を直して", true)

    @Test
    fun `不一致だけ確認し未設定や旧Gatewayや障害では開始を妨げない`() = runBlocking {
        for (verdict in listOf("mismatch", "match", "unknown", "disabled", "unavailable")) {
            assertEquals(verdict == "mismatch", needsDirectoryConfirmation(request) { DirectoryCheckResult(verdict) })
        }
        assertFalse(needsDirectoryConfirmation(request) { throw Exception("404") })
        assertFalse(needsDirectoryConfirmation(request.copy(prompt = "  ")) { error("called") })
    }

    @Test
    fun `画面を離れた時のキャンセルを起動許可に変えない`() = runBlocking {
        try {
            needsDirectoryConfirmation(request) { throw CancellationException("left screen") }
            fail("Cancellation must propagate")
        } catch (_: CancellationException) {
            // Expected: the caller cannot continue to startSession.
        }
    }
}
