package com.tohutohu.herdrcompanion.ui.detail

import com.tohutohu.herdrcompanion.data.api.SideAnswerDto
import com.tohutohu.herdrcompanion.data.api.SideTurnDto
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.getAndUpdate
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/** One question asked on the side (btw) and, once it came, its answer or why there is none. */
data class SideQuestionEntry(
    val id: Long,
    val question: String,
    val answer: String? = null,
    val error: String? = null,
) {
    val pending: Boolean get() = answer == null && error == null
}

/** One line of questions on the side: each follow-up has only its own lane's answers as context. */
data class SideLane(val id: Long, val entries: List<SideQuestionEntry>) {
    /** A lane is named by its first question. */
    val title: String get() = entries.first().question
    val pending: Boolean get() = entries.any { it.pending }
}

/**
 * A session's side conversations (lanes), the one shown, and whether the
 * sheet is open. A null [activeLane] is a new lane, which exists only once
 * something is asked in it.
 */
data class SideConversation(
    val lanes: List<SideLane> = emptyList(),
    val activeLane: Long? = null,
    val open: Boolean = false,
) {
    val active: SideLane? get() = lanes.firstOrNull { it.id == activeLane }

    /** The shown lane's questions. */
    val entries: List<SideQuestionEntry> get() = active?.entries.orEmpty()
}

/**
 * Questions asked on the side of a session (Claude Code's /btw). They are
 * answered from the session's conversation and never become part of it, so
 * they live only here, for as long as the app runs: the gateway keeps none,
 * and a follow-up sends the earlier answered questions of its lane as its
 * history. Lanes are asked independently, at the same time if need be.
 *
 * Questions are asked on [scope], so an answer still arrives after its sheet
 * was closed or the session left.
 */
class SideQuestions(
    private val scope: CoroutineScope,
    private val ask: suspend (sessionId: String, question: String, history: List<SideTurnDto>) -> SideAnswerDto,
) {
    private val state = MutableStateFlow<Map<String, SideConversation>>(emptyMap())
    private val nextId = MutableStateFlow(0L)

    val conversations: StateFlow<Map<String, SideConversation>> = state.asStateFlow()

    operator fun get(sessionId: String): SideConversation = state.value[sessionId] ?: SideConversation()

    fun setOpen(sessionId: String, open: Boolean) = edit(sessionId) { it.copy(open = open) }

    fun toggle(sessionId: String) = edit(sessionId) { it.copy(open = !it.open) }

    fun selectLane(sessionId: String, laneId: Long) = edit(sessionId) { c ->
        if (c.lanes.any { it.id == laneId }) c.copy(activeLane = laneId) else c
    }

    /** Shows a new, empty lane; the others keep going. */
    fun newLane(sessionId: String) = edit(sessionId) { it.copy(activeLane = null) }

    /** Asks in the shown lane, starting one if it is new. */
    fun ask(sessionId: String, question: String) {
        val text = question.trim()
        if (text.isEmpty()) return
        val id = nextId.getAndUpdate { it + 1 }
        val lane = get(sessionId).active
        // Earlier questions are its context; one still waiting or failed has nothing to add.
        val history = lane?.entries.orEmpty().history()
        val entry = SideQuestionEntry(id, text)
        edit(sessionId) { c ->
            if (lane == null) {
                // A lane takes the id of its first question.
                c.copy(lanes = c.lanes + SideLane(id, listOf(entry)), activeLane = id)
            } else {
                c.copy(lanes = c.lanes.map { if (it.id == lane.id) it.copy(entries = it.entries + entry) else it })
            }
        }
        run(sessionId, id, text, history)
    }

    /** Asks a failed question again, with the history it had. */
    fun retry(sessionId: String, entryId: Long) {
        val entries = get(sessionId).lanes.firstOrNull { lane -> lane.entries.any { it.id == entryId } }?.entries ?: return
        val index = entries.indexOfFirst { it.id == entryId }
        val entry = entries[index].takeIf { it.error != null } ?: return
        update(sessionId, entryId) { it.copy(error = null) }
        run(sessionId, entryId, entry.question, entries.take(index).history())
    }

    /** Drops the shown lane and shows the newest one left; the sheet stays as it is. */
    fun clear(sessionId: String) = edit(sessionId) { c ->
        val left = c.lanes.filter { it.id != c.activeLane }
        c.copy(lanes = left, activeLane = left.lastOrNull()?.id)
    }

    private fun List<SideQuestionEntry>.history() = mapNotNull { e -> e.answer?.let { SideTurnDto(e.question, it) } }

    private fun run(sessionId: String, entryId: Long, question: String, history: List<SideTurnDto>) {
        scope.launch {
            val result = try {
                Result.success(ask(sessionId, question, history))
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                Result.failure(e)
            }
            update(sessionId, entryId) { entry ->
                result.fold(
                    onSuccess = { entry.copy(answer = it.answer) },
                    onFailure = { entry.copy(error = it.message?.takeIf(String::isNotBlank) ?: "Could not get an answer.") },
                )
            }
        }
    }

    /** A cleared lane drops the answer of a question still out. */
    private fun update(sessionId: String, entryId: Long, change: (SideQuestionEntry) -> SideQuestionEntry) =
        edit(sessionId) { c ->
            c.copy(lanes = c.lanes.map { lane -> lane.copy(entries = lane.entries.map { if (it.id == entryId) change(it) else it }) })
        }

    private fun edit(sessionId: String, change: (SideConversation) -> SideConversation) {
        state.update { all ->
            val next = change(all[sessionId] ?: SideConversation())
            if (next == SideConversation()) all - sessionId else all + (sessionId to next)
        }
    }
}
