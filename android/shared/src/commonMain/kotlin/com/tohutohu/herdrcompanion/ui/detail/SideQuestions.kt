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

/** A session's side conversation, and whether its sheet is open. */
data class SideConversation(
    val entries: List<SideQuestionEntry> = emptyList(),
    val open: Boolean = false,
) {
    val pending: Boolean get() = entries.any { it.pending }
}

/**
 * Questions asked on the side of a session (Claude Code's /btw). They are
 * answered from the session's conversation and never become part of it, so
 * they live only here, for as long as the app runs: the gateway keeps none,
 * and a follow-up sends the earlier answered questions as its history.
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

    fun ask(sessionId: String, question: String) {
        val text = question.trim()
        if (text.isEmpty()) return
        val id = nextId.getAndUpdate { it + 1 }
        // Earlier questions are its context; one still waiting or failed has nothing to add.
        val history = get(sessionId).entries.mapNotNull { e -> e.answer?.let { SideTurnDto(e.question, it) } }
        edit(sessionId) { it.copy(entries = it.entries + SideQuestionEntry(id, text)) }
        run(sessionId, id, text, history)
    }

    /** Asks a failed question again, with the history it had. */
    fun retry(sessionId: String, entryId: Long) {
        val entries = get(sessionId).entries
        val index = entries.indexOfFirst { it.id == entryId }
        val entry = entries.getOrNull(index)?.takeIf { it.error != null } ?: return
        val history = entries.take(index).mapNotNull { e -> e.answer?.let { SideTurnDto(e.question, it) } }
        update(sessionId, entryId) { it.copy(error = null) }
        run(sessionId, entryId, entry.question, history)
    }

    /** Starts the side conversation over; the sheet stays as it is. */
    fun clear(sessionId: String) = edit(sessionId) { it.copy(entries = emptyList()) }

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

    /** A cleared conversation drops the answer of a question still out. */
    private fun update(sessionId: String, entryId: Long, change: (SideQuestionEntry) -> SideQuestionEntry) =
        edit(sessionId) { c -> c.copy(entries = c.entries.map { if (it.id == entryId) change(it) else it }) }

    private fun edit(sessionId: String, change: (SideConversation) -> SideConversation) {
        state.update { all ->
            val next = change(all[sessionId] ?: SideConversation())
            if (next == SideConversation()) all - sessionId else all + (sessionId to next)
        }
    }
}
