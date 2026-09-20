package com.tohutohu.herdrcompanion.ui.newsession

import com.tohutohu.herdrcompanion.data.api.DirectoryCheckResult
import com.tohutohu.herdrcompanion.data.api.StartSessionRequest
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.withTimeoutOrNull

/** Advisory only. An old/offline gateway or disabled Jev must not prevent launch. */
suspend fun needsDirectoryConfirmation(
    request: StartSessionRequest,
    check: suspend (StartSessionRequest) -> DirectoryCheckResult,
): Boolean {
    if (request.prompt.isBlank()) return false
    return try {
        withTimeoutOrNull(12_000) { check(request).verdict == "mismatch" } ?: false
    } catch (e: CancellationException) {
        throw e // Leaving the screen must never launch an agent afterwards.
    } catch (_: Exception) {
        false
    }
}
