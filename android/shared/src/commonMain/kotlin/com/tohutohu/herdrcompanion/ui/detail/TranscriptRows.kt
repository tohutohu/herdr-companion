package com.tohutohu.herdrcompanion.ui.detail

import com.tohutohu.herdrcompanion.data.Message
import com.tohutohu.herdrcompanion.data.api.BlockDto

/**
 * One item of the message list. A turn (an instruction and the agent's work
 * on it) reads as the instruction, one [WorkRow] holding the work, and the
 * agent's newest report. While the agent works, the tool calls after that
 * report stay on their own [ActivityRow], so its newest call still shows.
 */
sealed interface TranscriptRow {
    val key: String

    /** Index in the message list of the first message this row shows. */
    val messageIndex: Int

    /** How the turn ended, when this is its last row. */
    val end: TurnEnd?
}

/** How a turn ended; [at] is the time of its last message. */
data class TurnEnd(val failed: Boolean, val at: Long)

/** What the session tells about its newest turn; older turns are all over. */
enum class LastTurn { WORKING, DONE, FAILED }

/** A message, or the prose part of one whose tool calls went to a group. */
data class MessageRow(
    override val key: String,
    override val messageIndex: Int,
    val message: Message,
    override val end: TurnEnd? = null,
) : TranscriptRow

/** A run of tool calls and outputs, shown as one line until expanded. */
data class ActivityRow(
    override val key: String,
    override val messageIndex: Int,
    val parts: List<Message>,
    override val end: TurnEnd? = null,
) : TranscriptRow {
    val callCount get() = parts.sumOf { m -> m.blocks.count { it.isCall(m.role) } }

    /** The first line of the newest call (or output, before any call). */
    val latest: String
        get() {
            val texts = parts.flatMap { m -> m.blocks.filter { it.type == "text" && !it.text.isNullOrBlank() }.map { m.role to it } }
            val block = texts.lastOrNull { (role, b) -> b.isCall(role) }?.second ?: texts.lastOrNull()?.second
            return block?.text?.lineSequence()?.map { it.trim() }?.firstOrNull { it.isNotEmpty() } ?: "Tool output"
        }
}

/**
 * The work of a turn before its newest report (earlier reports, tool calls
 * and their outputs) folded to one line. [rows] are how the list would show
 * them. [durationMs] is how long the turn took, once it is over and known.
 */
data class WorkRow(
    override val key: String,
    override val messageIndex: Int,
    val rows: List<TranscriptRow>,
    val working: Boolean,
    val durationMs: Long?,
    override val end: TurnEnd? = null,
) : TranscriptRow {
    val callCount get() = rows.sumOf { (it as? ActivityRow)?.callCount ?: 0 }
}

/** Whether [row] shows the message [id], folded or not. */
fun TranscriptRow.shows(id: String): Boolean = when (this) {
    is MessageRow -> message.id == id
    is ActivityRow -> parts.any { it.id == id }
    is WorkRow -> rows.any { it.shows(id) }
}

/**
 * Splits [messages] into rows. [messageKeys] holds the stable keys of
 * messages that replaced an optimistic pending row. [lastTurn] tells whether
 * the newest turn is still going; until it is over its newest report stays
 * out, and only then does that report become its answer.
 */
fun transcriptRows(
    messages: List<Message>,
    messageKeys: Map<String, String> = emptyMap(),
    lastTurn: LastTurn = LastTurn.WORKING,
): List<TranscriptRow> {
    val pieces = messages.flatMapIndexed { index, message ->
        activityRuns(message).map { (start, activity, blocks) ->
            val part = if (start == 0 && blocks.size == message.blocks.size) message else message.copy(blocks = blocks)
            Piece(index, start, activity, part)
        }
    }
    val starts = pieces.indices.filter { pieces[it].isInstruction }
    val bounds = (listOf(0) + starts + pieces.size).distinct()
    val rows = mutableListOf<TranscriptRow>()
    bounds.zipWithNext().forEach { (from, to) ->
        val turn = pieces.subList(from, to)
        val newest = to == pieces.size
        val head = turn.first().takeIf { it.isInstruction }
        rows += turnRows(
            head = head,
            body = if (head != null) turn.drop(1) else turn,
            over = !newest || lastTurn != LastTurn.WORKING,
            failed = newest && lastTurn == LastTurn.FAILED,
            messageKeys = messageKeys,
        )
    }
    return uniqueKeys(rows)
}

/** A run of blocks of the message at [index] (see [activityRuns]). */
private data class Piece(val index: Int, val start: Int, val activity: Boolean, val message: Message) {
    val isInstruction get() = message.role == "user" && !message.queued

    /** Something the agent wrote for the reader, rather than its work. */
    val isReport get() = !activity && message.role == "assistant" && message.blocks.none { it.type == "interaction" }

    val asksReader get() = message.blocks.any { it.interaction?.isPending == true }
}

private fun turnRows(
    head: Piece?,
    body: List<Piece>,
    over: Boolean,
    failed: Boolean,
    messageKeys: Map<String, String>,
): List<TranscriptRow> {
    // A prompt the agent has not taken in yet is not part of the turn's work.
    val (queued, work) = body.partition { it.message.queued }
    // The newest report stays out of the fold, and so does a question waiting
    // for the reader, with everything after it. Without either, a running
    // turn shows its work as it goes and a finished one folds all of it.
    val split = listOf(work.indexOfLast { it.isReport }, work.indexOfFirst { it.asksReader })
        .filter { it >= 0 }
        .minOrNull() ?: if (over) work.size else 0
    val folded = work.subList(0, split)
    val shown = work.subList(split, work.size)

    val rows = mutableListOf<TranscriptRow>()
    head?.let { rows += groupRows(listOf(it), messageKeys) }
    val durationMs = if (over && head != null && work.isNotEmpty()) {
        val start = head.message.timestamp
        val finish = work.last().message.timestamp
        (finish - start).takeIf { start > 0 && it > 0 }
    } else {
        null
    }
    if (folded.isNotEmpty() || durationMs != null) {
        rows += WorkRow(
            key = "work:" + (head ?: work.first()).let { "${it.message.id}#${it.start}" },
            messageIndex = (folded.firstOrNull() ?: shown.first()).index,
            rows = groupRows(folded, messageKeys),
            working = !over,
            durationMs = durationMs,
        )
    }
    rows += groupRows(shown, messageKeys)
    if (over && work.isNotEmpty()) {
        rows[rows.lastIndex] = rows.last().withEnd(TurnEnd(failed, work.last().message.timestamp))
    }
    rows += groupRows(queued, messageKeys)
    return rows
}

/** Rows as the pieces read: consecutive activity shares one row. */
private fun groupRows(pieces: List<Piece>, messageKeys: Map<String, String>): List<TranscriptRow> {
    val rows = mutableListOf<TranscriptRow>()
    var group: ActivityRow? = null
    fun flush() {
        group?.let(rows::add)
        group = null
    }
    pieces.forEach { (index, start, activity, part) ->
        if (activity) {
            val g = group
            group = g?.copy(parts = g.parts + part) ?: ActivityRow("tools:${part.id}#$start", index, listOf(part))
        } else {
            flush()
            val key = if (start == 0) messageKeys[part.id] ?: "message:${part.id}" else "message:${part.id}#$start"
            rows += MessageRow(key, index, part)
        }
    }
    flush()
    return rows
}

private fun TranscriptRow.withEnd(end: TurnEnd): TranscriptRow = when (this) {
    is MessageRow -> copy(end = end)
    is ActivityRow -> copy(end = end)
    is WorkRow -> copy(end = end)
}

/**
 * Lazy lists crash on a repeated key, so a row whose key is already taken
 * (messages with the same or no ID) gets its position appended.
 */
private fun uniqueKeys(rows: List<TranscriptRow>): List<TranscriptRow> {
    val seen = HashSet<String>()
    return rows.mapIndexed { i, row ->
        val unique = if (row is WorkRow) row.copy(rows = uniqueKeys(row.rows)) else row
        if (seen.add(unique.key)) {
            unique
        } else {
            val key = "${unique.key}@$i"
            seen += key
            when (unique) {
                is MessageRow -> unique.copy(key = key)
                is ActivityRow -> unique.copy(key = key)
                is WorkRow -> unique.copy(key = key)
            }
        }
    }
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
    // Questions and approvals are for the reader; never fold them into a tool line.
    block.type == "interaction" -> false
    role == "tool" -> true
    role == "assistant" -> block.type == "text" && isToolCallText(block.text.orEmpty())
    else -> false
}

private fun BlockDto.isCall(role: String) =
    type == "text" && (role == "assistant" || role == "tool") && isToolCallText(text.orEmpty())

/** "45s", "1m 26s", "1h 05m": how long a turn took. */
fun durationLabel(ms: Long): String {
    val s = ((ms + 500) / 1000).coerceAtLeast(1)
    return when {
        s < 60 -> "${s}s"
        s < 3600 -> "${s / 60}m ${(s % 60).toString().padStart(2, '0')}s"
        else -> "${s / 3600}h ${(s % 3600 / 60).toString().padStart(2, '0')}m"
    }
}
