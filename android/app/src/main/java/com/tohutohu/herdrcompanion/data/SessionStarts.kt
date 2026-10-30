package com.tohutohu.herdrcompanion.data

import com.tohutohu.herdrcompanion.data.api.StartSessionRequest
import com.tohutohu.herdrcompanion.data.api.StartSessionResponse
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.util.UUID

data class SessionStart(
    val id: String,
    val request: StartSessionRequest,
    val busy: Boolean = true,
    /** The launch id a folder-trust answer goes to, while the gateway waits for one. */
    val trustLaunch: String? = null,
    val sessionId: String? = null,
    val notice: String? = null,
    val listed: Boolean = false,
) {
    val label: String get() = when {
        busy -> "Starting…"
        trustLaunch != null -> "Folder trust required"
        sessionId != null -> "Started"
        else -> notice ?: "Session started; waiting for it to appear in the list."
    }
}

/** Application-owned launches survive leaving the form and configuration changes. */
class SessionStarts(
    private val scope: CoroutineScope,
    private val start: suspend (StartSessionRequest) -> StartSessionResponse,
    private val answer: suspend (String, Boolean) -> StartSessionResponse,
    private val refresh: suspend () -> Unit,
) {
    private val mutable = MutableStateFlow<List<SessionStart>>(emptyList())
    val entries = mutable.asStateFlow()

    /**
     * Starts a session. [uploads] uploads the first prompt's attachments and
     * returns their ids; when it fails, the session is not started.
     */
    fun enqueue(request: StartSessionRequest, uploads: suspend () -> List<String> = { emptyList() }): String {
        val entry = SessionStart(UUID.randomUUID().toString(), request)
        mutable.update { listOf(entry) + it }
        execute(entry.id) {
            val ids = try {
                uploads()
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                throw UploadFailed(e)
            }
            start(if (ids.isEmpty()) request else request.copy(uploads = ids))
        }
        return entry.id
    }

    fun answerTrust(id: String, trust: Boolean) {
        val entry = mutable.value.find { it.id == id } ?: return
        val launch = entry.trustLaunch ?: return
        if (entry.busy) return
        change(id) { it.copy(busy = true, trustLaunch = null, notice = null) }
        execute(id) {
            val result = answer(launch, trust)
            if (!trust) result.copy(launchId = null, warning = "Session start cancelled.") else result
        }
    }

    /** Keep a partial resume reachable from the session list as well. */
    fun trackResume(request: StartSessionRequest, result: StartSessionResponse) {
        val entry = SessionStart(UUID.randomUUID().toString(), request, busy = false,
            trustLaunch = result.launchId?.takeIf { result.trustRequired && it.isNotEmpty() },
            sessionId = result.sessionId, notice = result.warning)
        mutable.update { listOf(entry) + it }
    }

    fun reconcile(sessionIds: Set<String>) {
        mutable.update { rows -> rows.map {
            if (!it.busy && it.trustLaunch == null && it.sessionId in sessionIds) it.copy(listed = true) else it
        } }
    }

    fun dismiss(id: String) {
        mutable.update { rows -> rows.filterNot { it.id == id && !it.busy && it.trustLaunch == null } }
    }

    private fun change(id: String, block: (SessionStart) -> SessionStart) {
        mutable.update { rows -> rows.map { if (it.id == id) block(it) else it } }
    }

    private fun execute(id: String, call: suspend () -> StartSessionResponse) {
        scope.launch {
            try {
                val res = call()
                change(id) {
                    it.copy(
                        busy = false,
                        trustLaunch = res.launchId?.takeIf { res.trustRequired && it.isNotEmpty() },
                        sessionId = res.sessionId,
                        notice = res.warning,
                    )
                }
                // A refresh failure must not turn a successful launch into a failed one.
                try { refresh() } catch (e: CancellationException) { throw e } catch (_: Exception) { }
            } catch (e: CancellationException) {
                throw e
            } catch (e: UploadFailed) {
                change(id) { it.copy(busy = false, notice = "Could not upload the attachments: ${e.cause?.message}. The session was not started.") }
            } catch (e: Exception) {
                change(id) { it.copy(busy = false, notice = "Could not confirm session start: ${e.message}. Check the session list before starting again.") }
            }
        }
    }

    /** Nothing was sent to the gateway yet, so the session certainly did not start. */
    private class UploadFailed(cause: Exception) : Exception(cause)
}
