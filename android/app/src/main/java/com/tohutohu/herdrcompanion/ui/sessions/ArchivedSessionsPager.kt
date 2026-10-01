package com.tohutohu.herdrcompanion.ui.sessions

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.tohutohu.herdrcompanion.data.api.SessionDto
import com.tohutohu.herdrcompanion.data.api.SessionsResponse
import kotlin.coroutines.cancellation.CancellationException
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

/** Archived sessions read per request; the gateway summarizes each one. */
const val ARCHIVED_PAGE_SIZE = 30

/**
 * Loads archived sessions one page at a time. [fetch] reads `limit` sessions
 * starting at `offset`; a limit of 0 reads the rest.
 */
class ArchivedSessionsPager(
    private val pageSize: Int = ARCHIVED_PAGE_SIZE,
    private val fetch: suspend (offset: Int, limit: Int) -> SessionsResponse,
) {
    var sessions by mutableStateOf<List<SessionDto>?>(null)
        private set

    // Where the next page starts; null once every archived session is loaded.
    var nextOffset by mutableStateOf<Int?>(null)
        private set

    // A failed page stops loading more until the list is reloaded.
    var moreFailed by mutableStateOf(false)
        private set

    val hasMore: Boolean get() = nextOffset != null && !moreFailed

    private val mutex = Mutex()

    /**
     * Reloads from the top. keepLoaded reloads as far as the list had been
     * loaded, so changing a session further down does not cut the list short.
     */
    suspend fun load(keepLoaded: Boolean) = mutex.withLock {
        val limit = when {
            !keepLoaded || sessions == null -> pageSize
            else -> nextOffset?.coerceAtLeast(pageSize) ?: 0
        }
        val page = fetch(0, limit)
        sessions = page.sessions
        nextOffset = page.nextOffset
        moreFailed = false
    }

    /** Appends the next page, if any. A failure is rethrown once. */
    suspend fun loadMore() = mutex.withLock {
        val offset = nextOffset ?: return@withLock
        if (moreFailed) return@withLock
        val page = try {
            fetch(offset, pageSize)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            moreFailed = true
            throw e
        }
        // A session archived meanwhile shifts the pages; keep ids unique.
        sessions = (sessions.orEmpty() + page.sessions).distinctBy { it.id }
        nextOffset = page.nextOffset
    }
}
