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
    val trustPane: String? = null,
    val paneId: String? = null,
    val sessionId: String? = null,
    val needsContinue: Boolean = false,
    val notice: String? = null,
    val listed: Boolean = false,
) {
    val label: String get() = when {
        busy -> "Starting…"
        trustPane != null -> "Folder trust required"
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
    private val continueStart: suspend (String) -> StartSessionResponse = { error("Continue is unavailable") },
) {
    private val mutable = MutableStateFlow<List<SessionStart>>(emptyList())
    val entries = mutable.asStateFlow()

    fun enqueue(request: StartSessionRequest): String {
        val entry = SessionStart(UUID.randomUUID().toString(), request)
        mutable.update { listOf(entry) + it }
        execute(entry.id) { start(request) }
        return entry.id
    }

    fun answerTrust(id: String, trust: Boolean) {
        val entry = mutable.value.find { it.id == id } ?: return
        val pane = entry.trustPane ?: return
        if (entry.busy) return
        change(id) { it.copy(busy = true, trustPane = null, notice = null) }
        execute(id) {
            val result = answer(pane, trust)
            if (!trust) result.copy(paneId = "", warning = "Session start cancelled.") else result
        }
    }

    fun continueLaunch(id: String) {
        val entry = mutable.value.find { it.id == id } ?: return
        val pane = entry.paneId ?: return
        if (entry.busy) return
        change(id) { it.copy(busy = true, notice = null) }
        execute(id) { continueStart(pane) }
    }

    /** Keep a partial resume reachable from the session list as well. */
    fun trackResume(request: StartSessionRequest, result: StartSessionResponse) {
        val entry = SessionStart(UUID.randomUUID().toString(), request, busy = false,
            paneId = result.paneId.takeIf { it.isNotEmpty() },
            trustPane = result.paneId.takeIf { result.trustRequired },
            sessionId = result.sessionId, needsContinue = result.sessionId == null, notice = result.warning)
        mutable.update { listOf(entry) + it }
    }

    fun reconcile(sessionIds: Set<String>, paneSessions: Map<String, String> = emptyMap()) {
        mutable.update { rows -> rows.map {
            val resolved = it.sessionId ?: it.paneId?.let(paneSessions::get)
            if (!it.busy && !it.needsContinue && it.trustPane == null && resolved in sessionIds) it.copy(sessionId = resolved, listed = true) else it
        } }
    }

    fun dismiss(id: String) {
        mutable.update { rows -> rows.filterNot { it.id == id && !it.busy && it.trustPane == null } }
    }

    private fun change(id: String, block: (SessionStart) -> SessionStart) {
        mutable.update { rows -> rows.map { if (it.id == id) block(it) else it } }
    }

    private fun execute(id: String, call: suspend () -> StartSessionResponse) {
        scope.launch {
            try {
                val res = call()
                change(id) { it.copy(busy = false, paneId = res.paneId.takeIf { it.isNotEmpty() }, trustPane = res.paneId.takeIf { res.trustRequired }, sessionId = res.sessionId, needsContinue = res.sessionId == null && res.warning != null && res.paneId.isNotEmpty(), notice = res.warning) }
                // A refresh failure must not turn a successful launch into a failed one.
                try { refresh() } catch (e: CancellationException) { throw e } catch (_: Exception) { }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                change(id) { it.copy(busy = false, notice = "Could not confirm session start: ${e.message}. Check the session list before starting again.") }
            }
        }
    }
}
