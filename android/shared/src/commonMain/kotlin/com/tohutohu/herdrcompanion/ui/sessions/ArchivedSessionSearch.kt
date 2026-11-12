package com.tohutohu.herdrcompanion.ui.sessions

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.tohutohu.herdrcompanion.data.api.SessionDto
import kotlin.coroutines.cancellation.CancellationException
import kotlinx.coroutines.delay

/**
 * Finds archived sessions with the gateway's search, so typing a query does
 * not load the whole archive. [find] returns null from a gateway that cannot
 * search; the caller then loads every page and filters them itself, as it
 * does when a search fails.
 */
class ArchivedSessionSearch(
    private val debounceMillis: Long = 250,
    private val find: suspend (query: String) -> List<SessionDto>?,
) {
    private var answered by mutableStateOf<String?>(null)
    private var results by mutableStateOf<List<SessionDto>>(emptyList())
    private var failed by mutableStateOf<String?>(null)
    private var unsupported by mutableStateOf(false)

    /** What the gateway found for [query]; null until it answers, and when it cannot. */
    fun resultsFor(query: String): List<SessionDto>? = results.takeIf { answered == query.trim() }

    /** The gateway cannot answer [query], so the answer is in the pages of the archive. */
    fun needsAllPages(query: String): Boolean = unsupported || failed == query.trim()

    /** Asks the gateway for [query] once typing pauses. Cancel it when the query changes. */
    suspend fun search(query: String) {
        val q = query.trim()
        failed = null
        if (q.isEmpty()) {
            answered = null
            results = emptyList()
            return
        }
        if (unsupported) return
        delay(debounceMillis)
        val found = try {
            find(q)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            failed = q
            return
        }
        if (found == null) {
            unsupported = true
            return
        }
        results = found
        answered = q
    }
}
