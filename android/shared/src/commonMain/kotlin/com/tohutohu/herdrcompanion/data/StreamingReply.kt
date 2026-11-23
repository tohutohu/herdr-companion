package com.tohutohu.herdrcompanion.data

import com.tohutohu.herdrcompanion.data.api.BlockDto

/** Id prefix of a reply still being written (the gateway's and the streamed copy's). */
const val STREAMING_PREFIX = "streaming:"

/**
 * The text so far of a reply an agent is writing, as the gateway's event
 * stream reported it. [done]: the reply is closed and the next fetch has it,
 * so this copy goes once that fetch is applied.
 */
data class StreamingReply(
    val messageId: String,
    val text: String,
    val done: Boolean,
    val startedAt: Long,
)

/**
 * Follows one session's streamed reply: what to show on top of the fetched
 * conversation until a fetch has the reply itself.
 */
class StreamingReplyState {
    var current: StreamingReply? = null
        private set

    /** Applies a streaming event; returns the new state. */
    fun onEvent(messageId: String, text: String, done: Boolean, now: Long): StreamingReply? {
        val previous = current?.takeIf { it.messageId == messageId }
        current = when {
            // Closing keeps the text until the conversation shows the reply;
            // it carries the final text, whose end may not have been sent yet.
            done -> previous?.copy(done = true, text = text.ifEmpty { previous.text })
            else -> StreamingReply(messageId, text, done = false, startedAt = previous?.startedAt ?: now)
        }
        return current
    }

    /** Forgets the reply, e.g. when the stream it came from was interrupted. */
    fun clear(): StreamingReply? {
        current = null
        return null
    }

    /** A fetch has been applied: a closed reply is in the conversation now. */
    fun onFetched(): StreamingReply? {
        if (current?.done == true) current = null
        return current
    }
}

/**
 * The conversation with [reply] shown as it is being written: it replaces the
 * text of the gateway's own `streaming:` message when it has more, or is added
 * after the last message that is not queued (queued prompts stay at the end).
 */
fun List<Message>.withStreaming(reply: StreamingReply?): List<Message> {
    if (reply == null || reply.text.isBlank()) return this
    val id = STREAMING_PREFIX + reply.messageId
    val index = indexOfFirst { it.id == id }
    if (index >= 0) {
        val existing = this[index]
        val shown = existing.blocks.sumOf { it.text?.length ?: 0 }
        if (reply.text.length <= shown) return this
        return toMutableList().also { it[index] = existing.copy(blocks = listOf(BlockDto(type = "text", text = reply.text))) }
    }
    val message = Message(id, "assistant", reply.startedAt, listOf(BlockDto(type = "text", text = reply.text)))
    var at = size
    while (at > 0 && this[at - 1].queued) at--
    return toMutableList().also { it.add(at, message) }
}
