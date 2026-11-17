package com.tohutohu.herdrcompanion.ui.input

import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue

/**
 * Markdown lists in a plain message field: Return carries the item on, Tab and
 * Shift+Tab nest it, and a list pasted in keeps its items and depth. The text
 * stays plain Markdown, which is what the agents read.
 */

/** One list item's leading part: `  - [ ] `. */
internal data class ListItemPrefix(
    val indent: String,
    val marker: String,
    val gap: String,
    val task: String,
) {
    val length: Int get() = indent.length + marker.length + gap.length + task.length
    val ordered: Boolean get() = marker.last() == '.' || marker.last() == ')'
    val number: Int? get() = if (ordered) marker.dropLast(1).toIntOrNull() else null
    val indentWidth: Int get() = indentWidth(indent)

    /** Where a wrapped line of this item's text starts. */
    val contentColumn: Int get() = indentWidth + marker.length + gap.length
}

private val ITEM = Regex("""^([ \t]*)([-*+]|\d{1,9}[.)])([ \t]+|$)(\[[ xX]](?:[ \t]+|$))?""")

internal fun parseListItem(line: String): ListItemPrefix? {
    val m = ITEM.find(line) ?: return null
    val (indent, marker, gap, task) = m.destructured
    // "-" alone is an item still being typed only once it has its space.
    if (gap.isEmpty()) return null
    return ListItemPrefix(indent, marker, gap, task)
}

internal fun indentWidth(indent: String): Int = indent.sumOf { if (it == '\t') 4 else 1 }

private fun lineStart(text: String, offset: Int): Int = text.lastIndexOf('\n', offset - 1) + 1

private fun lineEnd(text: String, offset: Int): Int = text.indexOf('\n', offset).let { if (it < 0) text.length else it }

private fun nextMarker(marker: String): String {
    val number = marker.dropLast(1).toIntOrNull() ?: return marker
    return "${number + 1}${marker.last()}"
}

/**
 * Return on a list item. A filled item starts the next one at the same depth;
 * an empty one steps out a level, and at the top ends the list. Null when the
 * caret is not in an item's text, so the field breaks the line as usual.
 */
internal fun continueList(value: TextFieldValue): TextFieldValue? {
    val text = value.text
    val start = value.selection.min
    val end = value.selection.max
    val ls = lineStart(text, start)
    val le = lineEnd(text, start)
    val item = parseListItem(text.substring(ls, le)) ?: return null
    if (start - ls < item.length) return null
    val content = text.substring(ls + item.length, le)
    if (content.isBlank() && start == end) {
        if (item.indent.isNotEmpty()) return indentListItems(value, outdent = true)
        return TextFieldValue(text.replaceRange(ls, le, ""), TextRange(ls))
    }
    val next = item.indent + nextMarker(item.marker) + item.gap + if (item.task.isNotEmpty()) "[ ] " else ""
    val inserted = "\n" + next
    val edited = text.replaceRange(start, end, inserted)
    val caret = start + inserted.length
    return TextFieldValue(renumberAfter(edited, caret), TextRange(caret))
}

/** Shift+Return in an item: a new line inside the same item, aligned with its text. */
internal fun breakInsideListItem(value: TextFieldValue): TextFieldValue? {
    val text = value.text
    val start = value.selection.min
    val ls = lineStart(text, start)
    val item = parseListItem(text.substring(ls, lineEnd(text, start))) ?: return null
    if (start - ls < item.length) return null
    val inserted = "\n" + " ".repeat(item.contentColumn)
    val caret = start + inserted.length
    return TextFieldValue(text.replaceRange(start, value.selection.max, inserted), TextRange(caret))
}

/**
 * Following ordered items of the list the caret's line belongs to count on
 * from it, so inserting an item in the middle does not leave two "3."s.
 */
private fun renumberAfter(text: String, offset: Int): String {
    val lines = text.split('\n').toMutableList()
    var index = text.substring(0, offset).count { it == '\n' }
    val first = parseListItem(lines[index]) ?: return text
    var number = first.number ?: return text
    val width = first.indentWidth
    val delimiter = first.marker.last()
    while (++index < lines.size) {
        val line = lines[index]
        if (line.isBlank()) break
        val item = parseListItem(line)
        val lineIndent = indentWidth(line.takeWhile { it == ' ' || it == '\t' })
        if (item == null) {
            if (lineIndent > width) continue
            break
        }
        if (item.indentWidth > width) continue
        if (item.indentWidth < width || item.number == null || item.marker.last() != delimiter) break
        number++
        if (item.number == number) continue
        lines[index] = item.indent + "$number$delimiter" + line.substring(item.indent.length + item.marker.length)
    }
    return lines.joinToString("\n")
}

/**
 * Tab / Shift+Tab: nests the list items the selection touches under the item
 * above, or brings them out a level. Null when it touches no item, leaving
 * the key to the field.
 */
internal fun indentListItems(value: TextFieldValue, outdent: Boolean): TextFieldValue? {
    val text = value.text
    val lines = text.split('\n').toMutableList()
    val starts = IntArray(lines.size)
    for (i in 1 until lines.size) starts[i] = starts[i - 1] + lines[i - 1].length + 1
    val selMin = value.selection.min
    val selMax = value.selection.max
    val firstLine = text.substring(0, selMin).count { it == '\n' }
    var lastLine = text.substring(0, selMax).count { it == '\n' }
    // A selection ending at a line's very start leaves that line alone.
    if (lastLine > firstLine && selMax == starts[lastLine]) lastLine--

    class Edit(val at: Int, val removed: Int, val inserted: Int)
    val edits = mutableListOf<Edit>()
    var touchesItem = false
    for (i in firstLine..lastLine) {
        val item = parseListItem(lines[i]) ?: continue
        touchesItem = true
        val current = item.indentWidth
        val target = if (outdent) {
            if (current == 0) continue
            (i - 1 downTo 0).asSequence()
                .takeWhile { lines[it].isNotBlank() }
                .mapNotNull { parseListItem(lines[it]) }
                .firstOrNull { it.indentWidth < current }
                ?.indentWidth ?: 0
        } else {
            val above = (i - 1 downTo 0).asSequence()
                .takeWhile { lines[it].isNotBlank() }
                .mapNotNull { parseListItem(lines[it]) }
                .firstOrNull { it.indentWidth <= current }
            if (above != null && above.indentWidth == current) above.contentColumn else current + item.marker.length + item.gap.length
        }
        val marker = if (item.ordered) "${siblingNumber(lines, i, target)}${item.marker.last()}" else item.marker
        val oldPrefix = item.indent + item.marker
        val newPrefix = " ".repeat(target) + marker
        lines[i] = newPrefix + lines[i].substring(oldPrefix.length)
        edits += Edit(starts[i], oldPrefix.length, newPrefix.length)
    }
    if (!touchesItem) return null
    // Shift+Tab at the top level still keeps the key, rather than moving focus.
    if (edits.isEmpty()) return value
    fun map(offset: Int): Int {
        var shifted = offset
        for (e in edits) {
            when {
                offset >= e.at + e.removed -> shifted += e.inserted - e.removed
                offset > e.at -> shifted += (e.at + e.inserted) - offset
            }
        }
        return shifted
    }
    val selection = TextRange(map(value.selection.start), map(value.selection.end))
    return TextFieldValue(lines.joinToString("\n"), selection)
}

/** The number an ordered item takes when moved to [indent]: one past the sibling above. */
private fun siblingNumber(lines: List<String>, index: Int, indent: Int): Int {
    for (i in index - 1 downTo 0) {
        if (lines[i].isBlank()) break
        val item = parseListItem(lines[i]) ?: continue
        if (item.indentWidth < indent) break
        if (item.indentWidth == indent) return (item.number ?: return 1) + 1
    }
    return 1
}

/**
 * The field's own edits, looked at as they arrive: a line break typed on a
 * phone keyboard carries the list on like Return does, and a list pasted as
 * text (•, ・ or tab-indented bullets) comes in as Markdown items.
 */
internal fun applyListTyping(old: TextFieldValue, new: TextFieldValue): TextFieldValue {
    if (new.text == old.text) return new
    val start = old.selection.min
    val end = old.selection.max
    val insertedLength = new.text.length - (old.text.length - (end - start))
    if (insertedLength <= 0) return new
    if (new.selection != TextRange(start + insertedLength)) return new
    if (!new.text.regionMatches(0, old.text, 0, start)) return new
    if (!new.text.regionMatches(start + insertedLength, old.text, end, old.text.length - end)) return new
    val inserted = new.text.substring(start, start + insertedLength)
    if (inserted == "\n") return continueList(old) ?: new
    if (insertedLength > 1) return pasteList(old, inserted) ?: new
    return new
}

/** A paste of [inserted] over [old]'s selection, when it holds a list to tidy. */
internal fun pasteList(old: TextFieldValue, inserted: String): TextFieldValue? {
    var pasted = normalizePastedList(inserted)
    val text = old.text
    val start = old.selection.min
    var from = start
    val ls = lineStart(text, start)
    val before = text.substring(ls, start)
    val emptyItem = parseListItem(before)?.takeIf { it.length == before.length }
    if (emptyItem != null && parseListItem(pasted.substringBefore('\n')) != null) {
        // Pasted onto an empty item: the list takes its place, at its depth.
        pasted = pasted.split('\n').joinToString("\n") { if (it.isEmpty()) it else emptyItem.indent + it }
        from = ls
    }
    if (pasted == inserted && from == start) return null
    val caret = from + pasted.length
    return TextFieldValue(text.replaceRange(from, old.selection.max, pasted), TextRange(caret))
}
