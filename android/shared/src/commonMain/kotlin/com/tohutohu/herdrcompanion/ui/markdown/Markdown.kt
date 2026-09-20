package com.tohutohu.herdrcompanion.ui.markdown

/**
 * A small Markdown subset parser for agent messages. It stays a pure data
 * transform so it can be unit tested without Compose; [MarkdownText] renders
 * the blocks.
 *
 * Soft line breaks are kept (chat-style): agents put meaningful newlines in
 * prose, so lines are not reflowed into a single paragraph.
 */
sealed interface MdBlock {
    data class Paragraph(val spans: List<MdSpan>) : MdBlock

    /** ATX heading. The renderer keeps the `#` marks visible as an outline cue. */
    data class Heading(val level: Int, val spans: List<MdSpan>) : MdBlock

    /** One list item. [marker] is already formatted ("•", "1.", "☐", "☑"). */
    data class ListItem(val depth: Int, val marker: String, val spans: List<MdSpan>) : MdBlock

    data class CodeBlock(val language: String?, val code: String) : MdBlock

    data class Quote(val blocks: List<MdBlock>) : MdBlock

    /** A pipe table. Each cell contains independently parsed inline spans. */
    data class Table(
        val header: List<List<MdSpan>>,
        val rows: List<List<List<MdSpan>>>,
        val alignments: List<MdTableAlignment>,
    ) : MdBlock

    data object Rule : MdBlock
}

enum class MdTableAlignment {
    Start,
    Center,
    End,
}

/** A run of text with inline styling. */
data class MdSpan(
    val text: String,
    val bold: Boolean = false,
    val italic: Boolean = false,
    val code: Boolean = false,
    val strike: Boolean = false,
    val link: String? = null,
)

private val HEADING = Regex("""^ {0,3}(#{1,6})\s+(.*?)(?:\s+#+)?\s*$""")
private val RULE = Regex("""^ {0,3}([-*_])\s*(?:\1\s*){2,}$""")
private val FENCE = Regex("""^ {0,3}(```+|~~~+)\s*(\S*).*$""")
private val QUOTE = Regex("""^ {0,3}>\s?(.*)$""")
private val LIST_ITEM = Regex("""^(\s*)(?:([-*+])|(\d{1,9})[.)])\s+(.*)$""")
private val TASK = Regex("""^\[([ xX])]\s+(.*)$""")
private val TABLE_SEPARATOR = Regex("""^:?-+:?$""")

private data class ParsedTable(
    val block: MdBlock.Table,
    val nextLine: Int,
)

/** Parses [src] into blocks. Anything unrecognized stays literal text. */
fun parseMarkdown(src: String): List<MdBlock> {
    val lines = src.trimEnd().lines()
    val blocks = mutableListOf<MdBlock>()
    val paragraph = mutableListOf<String>()
    // Indent columns of the open list levels, so both 2- and 4-space nesting work.
    val listIndents = mutableListOf<Int>()

    fun flushParagraph() {
        if (paragraph.isNotEmpty()) {
            blocks += MdBlock.Paragraph(parseInline(paragraph.joinToString("\n")))
            paragraph.clear()
        }
    }

    var i = 0
    while (i < lines.size) {
        val line = lines[i]
        val fence = FENCE.matchEntire(line)
        val table = parseTableAt(lines, i)
        when {
            fence != null -> {
                flushParagraph()
                listIndents.clear()
                val marker = fence.groupValues[1]
                val language = fence.groupValues[2].takeIf { it.isNotEmpty() }
                val code = mutableListOf<String>()
                i++
                while (i < lines.size && !lines[i].trimStart().startsWith(marker.take(3))) {
                    code += lines[i]
                    i++
                }
                if (i < lines.size) i++ // closing fence
                blocks += MdBlock.CodeBlock(language, code.joinToString("\n").trimEnd())
            }

            line.isBlank() -> {
                flushParagraph()
                listIndents.clear()
                i++
            }

            RULE.matches(line) -> {
                flushParagraph()
                listIndents.clear()
                blocks += MdBlock.Rule
                i++
            }

            HEADING.matchEntire(line) != null -> {
                flushParagraph()
                listIndents.clear()
                val m = HEADING.matchEntire(line)!!
                blocks += MdBlock.Heading(m.groupValues[1].length, parseInline(m.groupValues[2]))
                i++
            }

            QUOTE.matchEntire(line) != null -> {
                flushParagraph()
                listIndents.clear()
                val quoted = mutableListOf<String>()
                while (i < lines.size) {
                    val m = QUOTE.matchEntire(lines[i]) ?: break
                    quoted += m.groupValues[1]
                    i++
                }
                blocks += MdBlock.Quote(parseMarkdown(quoted.joinToString("\n")))
            }

            table != null -> {
                flushParagraph()
                listIndents.clear()
                blocks += table.block
                i = table.nextLine
            }

            LIST_ITEM.matchEntire(line) != null -> {
                flushParagraph()
                val m = LIST_ITEM.matchEntire(line)!!
                val indent = m.groupValues[1].replace("\t", "    ").length
                while (listIndents.isNotEmpty() && listIndents.last() > indent) listIndents.removeAt(listIndents.size - 1)
                if (listIndents.isEmpty() || listIndents.last() < indent) listIndents += indent
                val ordered = m.groupValues[3].isNotEmpty()
                var content = m.groupValues[4]
                var marker = if (ordered) "${m.groupValues[3]}." else "•"
                TASK.matchEntire(content)?.let { task ->
                    marker = if (task.groupValues[1].isBlank()) "☐" else "☑"
                    content = task.groupValues[2]
                }
                // Continuation lines of the same item: indented further, no marker.
                i++
                val continuation = mutableListOf(content)
                while (i < lines.size && lines[i].isNotBlank() && LIST_ITEM.matchEntire(lines[i]) == null) {
                    val next = lines[i]
                    if (next.takeWhile { it == ' ' }.length <= indent) break
                    if (FENCE.matchEntire(next) != null || HEADING.matchEntire(next) != null) break
                    continuation += next.trimStart()
                    i++
                }
                blocks += MdBlock.ListItem(listIndents.size - 1, marker, parseInline(continuation.joinToString("\n")))
            }

            else -> {
                listIndents.clear()
                paragraph += line
                i++
            }
        }
    }
    flushParagraph()
    return blocks
}

/** Finds a GitHub-style pipe table beginning at [start]. */
private fun parseTableAt(lines: List<String>, start: Int): ParsedTable? {
    val header = splitTableRow(lines[start]) ?: return null
    val separator = lines.getOrNull(start + 1)?.let(::splitTableRow) ?: return null
    if (header.size < 2 || separator.size < 2 || separator.any { !TABLE_SEPARATOR.matches(it) }) return null

    val columnCount = maxOf(header.size, separator.size)
    val normalizedHeader = normalizeTableCells(header, columnCount)
    val normalizedSeparator = normalizeTableCells(separator, columnCount)
    val alignments = normalizedSeparator.map { cell ->
        when {
            cell.startsWith(":") && cell.endsWith(":") -> MdTableAlignment.Center
            cell.endsWith(":") -> MdTableAlignment.End
            else -> MdTableAlignment.Start
        }
    }

    val rows = mutableListOf<List<List<MdSpan>>>()
    var i = start + 2
    while (i < lines.size && lines[i].isNotBlank()) {
        val row = splitTableRow(lines[i]) ?: break
        rows += normalizeTableCells(row, columnCount).map(::parseInline)
        i++
    }

    return ParsedTable(
        block = MdBlock.Table(
            header = normalizedHeader.map(::parseInline),
            rows = rows,
            alignments = alignments,
        ),
        nextLine = i,
    )
}

/**
 * Splits one table row while leaving escaped pipes and pipes inside inline code
 * in their cells. The backslashes are kept for [parseInline] to remove later.
 */
private fun splitTableRow(line: String): List<String>? {
    val trimmed = line.trim()
    if (!trimmed.contains('|')) return null

    var from = 0
    var to = trimmed.length
    if (trimmed.startsWith("|")) from++
    if (to > from && trimmed[to - 1] == '|' && !isEscaped(trimmed, to - 1)) to--

    val cells = mutableListOf<String>()
    val cell = StringBuilder()
    var codeDelimiter = 0
    var i = from
    while (i < to) {
        val c = trimmed[i]
        when {
            c == '\\' && codeDelimiter == 0 && i + 1 < to -> {
                cell.append(c).append(trimmed[i + 1])
                i += 2
            }

            c == '`' -> {
                val run = runLength(trimmed, i, '`')
                if (codeDelimiter == 0) codeDelimiter = run
                else if (codeDelimiter == run) codeDelimiter = 0
                cell.append(trimmed, i, i + run)
                i += run
            }

            c == '|' && codeDelimiter == 0 -> {
                cells += cell.toString().trim()
                cell.clear()
                i++
            }

            else -> {
                cell.append(c)
                i++
            }
        }
    }
    cells += cell.toString().trim()
    return cells.takeIf { it.size >= 2 }
}

private fun normalizeTableCells(cells: List<String>, columnCount: Int): List<String> = when {
    cells.size == columnCount -> cells
    cells.size < columnCount -> cells + List(columnCount - cells.size) { "" }
    else -> cells.take(columnCount - 1) + cells.drop(columnCount - 1).joinToString(" | ")
}

private fun isEscaped(text: String, index: Int): Boolean {
    var backslashes = 0
    var i = index - 1
    while (i >= 0 && text[i] == '\\') {
        backslashes++
        i--
    }
    return backslashes % 2 == 1
}

private const val ESCAPABLE = "\\`*_{}[]()#+-.!~>|"
private val AUTOLINK = Regex("""https?://[^\s<>"']+""")

/** Parses inline markup (code, emphasis, strikethrough, links) in [text]. */
fun parseInline(text: String): List<MdSpan> = parseInline(text, MdSpan(""))

private fun parseInline(text: String, base: MdSpan): List<MdSpan> {
    val out = mutableListOf<MdSpan>()
    val buffer = StringBuilder()

    fun flush() {
        if (buffer.isNotEmpty()) {
            out += base.copy(text = buffer.toString())
            buffer.clear()
        }
    }

    var i = 0
    while (i < text.length) {
        val c = text[i]
        when {
            c == '\\' && i + 1 < text.length && text[i + 1] in ESCAPABLE -> {
                buffer.append(text[i + 1])
                i += 2
            }

            c == '`' -> {
                val run = runLength(text, i, '`')
                val close = findRun(text, i + run, '`', run)
                if (close < 0) {
                    buffer.append(text, i, i + run)
                    i += run
                } else {
                    flush()
                    out += base.copy(text = text.substring(i + run, close).trim(' '), code = true)
                    i = close + run
                }
            }

            c == '[' -> {
                val link = matchLink(text, i)
                if (link == null) {
                    buffer.append(c)
                    i++
                } else {
                    flush()
                    // Agent output is untrusted: other schemes (intent:, tel:,
                    // another app's deep link) keep their label but open nothing.
                    out += parseInline(link.label, base.copy(link = link.url.takeIf { AUTOLINK.matchesAt(it, 0) }))
                    i = link.end
                }
            }

            c == '~' && runLength(text, i, '~') >= 2 -> {
                val close = findCloser(text, i + 2, '~', 2)
                if (close < 0) {
                    buffer.append("~~")
                    i += 2
                } else {
                    flush()
                    out += parseInline(text.substring(i + 2, close), base.copy(strike = true))
                    i = close + 2
                }
            }

            c == '*' || c == '_' -> {
                val run = runLength(text, i, c)
                val take = minOf(run, 3)
                val close = if (canOpen(text, i, c)) findCloser(text, i + take, c, take) else -1
                if (close < 0) {
                    buffer.append(text, i, i + run)
                    i += run
                } else {
                    flush()
                    val styled = when (take) {
                        3 -> base.copy(bold = true, italic = true)
                        2 -> base.copy(bold = true)
                        else -> base.copy(italic = true)
                    }
                    out += parseInline(text.substring(i + take, close), styled)
                    i = close + take
                }
            }

            c == 'h' && base.link == null && atWordStart(text, i) -> {
                val url = AUTOLINK.matchAt(text, i)?.value?.trimEnd('.', ',', ';', ':', '!', '?', ')', ']')
                if (url.isNullOrEmpty()) {
                    buffer.append(c)
                    i++
                } else {
                    flush()
                    out += base.copy(text = url, link = url)
                    i += url.length
                }
            }

            else -> {
                buffer.append(c)
                i++
            }
        }
    }
    flush()
    return out
}

private class Link(val label: String, val url: String, val end: Int)

/** Matches `[label](url)` starting at [start]. */
private fun matchLink(text: String, start: Int): Link? {
    var depth = 0
    var i = start
    while (i < text.length) {
        when (text[i]) {
            '\\' -> i++
            '[' -> depth++
            ']' -> {
                depth--
                if (depth == 0) break
            }
        }
        i++
    }
    if (i >= text.length || text.getOrNull(i + 1) != '(') return null
    val close = text.indexOf(')', i + 2)
    if (close < 0) return null
    val url = text.substring(i + 2, close).substringBefore(' ').trim()
    if (url.isEmpty()) return null
    return Link(text.substring(start + 1, i), url, close + 1)
}

private fun runLength(text: String, start: Int, c: Char): Int {
    var n = 0
    while (start + n < text.length && text[start + n] == c) n++
    return n
}

/** Index of the next run of exactly [len] or more [c] at or after [from]. */
private fun findRun(text: String, from: Int, c: Char, len: Int): Int {
    var i = from
    while (i < text.length) {
        if (text[i] == c) {
            val run = runLength(text, i, c)
            if (run >= len) return i
            i += run
        } else {
            i++
        }
    }
    return -1
}

/** Index of an emphasis closer: a delimiter run not preceded by whitespace. */
private fun findCloser(text: String, from: Int, c: Char, len: Int): Int {
    var i = from
    while (i < text.length) {
        if (text[i] == '\\') {
            i += 2
            continue
        }
        if (text[i] == c) {
            val run = runLength(text, i, c)
            if (run >= len && i > from && !text[i - 1].isWhitespace() && canClose(text, i + run, c)) return i
            i += run
        } else {
            i++
        }
    }
    return -1
}

/** `_` only delimits at word boundaries, so snake_case identifiers survive. */
private fun canOpen(text: String, i: Int, c: Char): Boolean {
    val next = text.getOrNull(i + runLength(text, i, c))
    if (next == null || next.isWhitespace()) return false
    if (c == '_') {
        val prev = text.getOrNull(i - 1)
        if (prev != null && prev.isLetterOrDigit()) return false
    }
    return true
}

private fun canClose(text: String, after: Int, c: Char): Boolean {
    if (c != '_') return true
    val next = text.getOrNull(after)
    return next == null || !next.isLetterOrDigit()
}

private fun atWordStart(text: String, i: Int): Boolean {
    val prev = text.getOrNull(i - 1)
    return prev == null || !prev.isLetterOrDigit()
}
