package com.tohutohu.herdrcompanion.ui.detail

import com.tohutohu.herdrcompanion.data.Message
import com.tohutohu.herdrcompanion.data.api.BlockDto

/**
 * One item of the message list. The agent's prose stays a message of its own;
 * the tool calls and outputs between two pieces of prose fold into one
 * [ActivityRow], so the reports read on without the work in between.
 */
sealed interface TranscriptRow {
    val key: String

    /** Index in the message list of the first message this row shows. */
    val messageIndex: Int

    /** The role used to decide where the role label goes. */
    val speaker: String
}

/** A message, or the prose part of one whose tool calls went to a group. */
data class MessageRow(
    override val key: String,
    override val messageIndex: Int,
    val message: Message,
) : TranscriptRow {
    override val speaker get() = if (message.role == "tool") "assistant" else message.role
}

/** A run of tool calls and outputs, shown as one line until expanded. */
data class ActivityRow(
    override val key: String,
    override val messageIndex: Int,
    val parts: List<Message>,
) : TranscriptRow {
    override val speaker get() = "assistant"

    val callCount get() = parts.sumOf { m -> m.blocks.count { it.isCall(m.role) } }

    /** The first line of the newest call (or output, before any call). */
    val latest: String
        get() {
            val texts = parts.flatMap { m -> m.blocks.filter { it.type == "text" && !it.text.isNullOrBlank() }.map { m.role to it } }
            val block = texts.lastOrNull { (role, b) -> b.isCall(role) }?.second ?: texts.lastOrNull()?.second
            return block?.text?.lineSequence()?.map { it.trim() }?.firstOrNull { it.isNotEmpty() } ?: "Tool output"
        }
}

/** Whether a row present in both [before] and [after] changed its content. */
fun rowsChangedInPlace(before: List<TranscriptRow>, after: List<TranscriptRow>): Boolean {
    if (before.isEmpty()) return false
    val old = before.associateBy { it.key }
    return after.any { row -> old[row.key]?.let { it != row } ?: false }
}

/**
 * Splits [messages] into rows. [messageKeys] holds the stable keys of
 * messages that replaced an optimistic pending row.
 */
fun transcriptRows(messages: List<Message>, messageKeys: Map<String, String> = emptyMap()): List<TranscriptRow> {
    val rows = mutableListOf<TranscriptRow>()
    var group: ActivityRow? = null
    fun flush() {
        group?.let(rows::add)
        group = null
    }
    messages.forEachIndexed { index, message ->
        activityRuns(message).forEach { (start, activity, blocks) ->
            val part = if (start == 0 && blocks.size == message.blocks.size) message else message.copy(blocks = blocks)
            if (activity) {
                val g = group
                group = g?.copy(parts = g.parts + part) ?: ActivityRow("tools:${message.id}#$start", index, listOf(part))
            } else {
                flush()
                val key = if (start == 0) messageKeys[message.id] ?: "message:${message.id}" else "message:${message.id}#$start"
                rows += MessageRow(key, index, part)
            }
        }
    }
    flush()
    return rows
}

private data class Run(val start: Int, val activity: Boolean, val blocks: List<BlockDto>)

/**
 * Contiguous blocks of [message] that are all activity or all not. A file
 * block goes with the block before it: the file a call read, or one the
 * prose mentions.
 */
private fun activityRuns(message: Message): List<Run> {
    val runs = mutableListOf<Run>()
    message.blocks.forEachIndexed { i, block ->
        val activity = if (block.type == "file") {
            runs.lastOrNull()?.activity ?: false
        } else {
            isActivity(message.role, block)
        }
        val last = runs.lastOrNull()
        if (last != null && last.activity == activity) {
            runs[runs.lastIndex] = last.copy(blocks = last.blocks + block)
        } else {
            runs += Run(i, activity, listOf(block))
        }
    }
    return runs
}

private fun isActivity(role: String, block: BlockDto): Boolean = when {
    // Questions and approvals wait for the reader; never fold them away.
    block.type == "interaction" -> false
    role == "tool" -> true
    role == "assistant" -> block.type == "text" && isToolCallText(block.text.orEmpty())
    else -> false
}

private fun BlockDto.isCall(role: String) =
    type == "text" && (role == "assistant" || role == "tool") && isToolCallText(text.orEmpty())
