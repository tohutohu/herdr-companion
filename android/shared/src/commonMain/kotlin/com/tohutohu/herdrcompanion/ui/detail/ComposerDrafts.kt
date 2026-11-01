package com.tohutohu.herdrcompanion.ui.detail

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.FlowPreview
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.debounce
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.drop
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.serialization.builtins.MapSerializer
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.json.Json

/**
 * What was typed into each session's composer and not sent yet, so leaving a
 * session (or the app) and coming back finds it again. Once typing pauses,
 * [persist] is handed every draft on [scope]; the platform writes them where
 * it keeps its local state. Without a scope the drafts last as long as this object.
 */
@OptIn(FlowPreview::class)
class ComposerDrafts(
    initial: Map<String, String> = emptyMap(),
    scope: CoroutineScope? = null,
    persist: (Map<String, String>) -> Unit = {},
) {
    private val drafts = MutableStateFlow(initial.filterValues { it.isNotEmpty() })

    init {
        // Subscribed before returning, so an edit made right away is not taken
        // for the initial value and skipped.
        scope?.launch(start = CoroutineStart.UNDISPATCHED) {
            drafts.drop(1).debounce(PERSIST_DELAY_MS).collect(persist)
        }
    }

    operator fun get(sessionId: String): String = drafts.value[sessionId].orEmpty()

    /** The draft as it changes, including text added from outside the composer. */
    fun observe(sessionId: String): Flow<String> = drafts.map { it[sessionId].orEmpty() }.distinctUntilChanged()

    /** Adds [text] after the draft, a blank line apart, for the user to edit before sending. */
    fun append(sessionId: String, text: String) {
        if (text.isBlank()) return
        val current = get(sessionId).trimEnd()
        set(sessionId, if (current.isEmpty()) text else "$current\n\n$text")
    }

    /** An empty [text] forgets the draft; the most recently edited [MAX_DRAFTS] are kept. */
    operator fun set(sessionId: String, text: String) {
        drafts.update { current ->
            if (text.isEmpty()) {
                current - sessionId
            } else {
                // Re-inserted last, so the oldest edit is the first to go.
                (current - sessionId + (sessionId to text)).entries.toList().takeLast(MAX_DRAFTS).associate { it.toPair() }
            }
        }
    }

    companion object {
        const val PERSIST_DELAY_MS = 300L
        const val MAX_DRAFTS = 100

        private val serializer = MapSerializer(String.serializer(), String.serializer())

        fun encode(drafts: Map<String, String>): String = Json.encodeToString(serializer, drafts)

        /** A missing or unreadable file starts with no drafts. */
        fun decode(text: String?): Map<String, String> =
            text?.let { runCatching { Json.decodeFromString(serializer, it) }.getOrNull() }.orEmpty()
    }
}
