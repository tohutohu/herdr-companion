package com.tohutohu.herdrmobile.ui.detail

import com.tohutohu.herdrmobile.data.Message

private const val MAX_ENTRY_CHARS = 240

data class FlowEntry(val messageId: String, val text: String)

/**
 * What stays pinned above the message list: the instruction of the turn on
 * screen and the agent's reports of that turn that scrolled off the top.
 */
data class FlowStack(val instruction: FlowEntry?, val reports: List<FlowEntry>) {
    val isEmpty get() = instruction == null && reports.isEmpty()

    companion object {
        val EMPTY = FlowStack(null, emptyList())
    }
}

/**
 * [firstVisible] is the index of the topmost message on screen. Messages
 * before it count as scrolled off. Tool calls and outputs are left out, so
 * at the bottom of a long turn the stack reads as its outline.
 */
fun flowStack(messages: List<Message>, firstVisible: Int): FlowStack {
    if (messages.isEmpty()) return FlowStack.EMPTY
    val top = firstVisible.coerceIn(0, messages.size)
    var turnStart = -1
    for (i in minOf(top, messages.lastIndex) downTo 0) {
        if (messages[i].role == "user") {
            turnStart = i
            break
        }
    }
    // The instruction itself is on screen: nothing of its turn is hidden yet.
    if (turnStart == top) return FlowStack.EMPTY
    val instruction = messages.getOrNull(turnStart)?.let { FlowEntry(it.id, entryText(it).ifEmpty { "(attachment)" }) }
    val reports = (turnStart + 1 until top).mapNotNull { i ->
        val m = messages[i]
        if (m.role != "assistant") return@mapNotNull null
        entryText(m).takeIf { it.isNotEmpty() }?.let { FlowEntry(m.id, it) }
    }
    return FlowStack(instruction, reports)
}

/** Prose of a message on one line; tool calls are not prose. */
private fun entryText(message: Message): String =
    message.blocks.asSequence()
        .filter { it.type == "text" }
        .mapNotNull { it.text }
        .filter { message.role != "assistant" || !isToolCallText(it) }
        .joinToString(" ")
        .replace(Regex("\\s+"), " ")
        .trim()
        .take(MAX_ENTRY_CHARS)
