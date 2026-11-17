package com.tohutohu.herdrcompanion.ui.input

/**
 * Lists copied from elsewhere, turned into Markdown items. Pages and documents
 * put their lists on the clipboard as HTML, with the plain text either losing
 * the bullets (browsers) or writing them as "•"/"・" with tabs for depth.
 */

private const val SPACE = " \t 　"
private val DEPTH_GLYPHS = listOf('•', '◦', '‣')
private const val BULLETS = "•◦▪▫●○■□◆◇‣⁃∙·・➢➤▸▹►☐☑☒"
private val GLYPH_ITEM = Regex("""^([$SPACE]*)[$BULLETS][$SPACE]*(\S.*)$""")
private val GLYPH_ONLY = Regex("""^([$SPACE]*)[$BULLETS][$SPACE]*$""")
private val NUMBER_ONLY = Regex("""^[$SPACE]*(\d{1,9}[.)])[$SPACE]*$""")

/** Tabbed numbers ("\t1.\tOne") come from rich-text apps; "1. One" is Markdown already. */
private val TABBED_ORDERED = Regex("""^([$SPACE]*)(\d{1,9}[.)])\t[$SPACE]*(\S.*)$""")

private fun spaceWidth(indent: String): Int = indent.sumOf {
    when (it) {
        '\t' -> 4
        '　' -> 2
        else -> 1
    }.toInt()
}

/**
 * Bullet characters and tab-indented numbers become "- " and "1. " items,
 * nested by how far each was indented. Text without them is left as it is.
 */
internal fun normalizePastedList(text: String): String {
    val lines = text.replace("\r\n", "\n").replace('\r', '\n').split('\n').toMutableList()
    // A selection copied across a rendered list can put each bullet on its own line.
    var i = 0
    while (i < lines.size - 1) {
        val only = GLYPH_ONLY.find(lines[i])
        if (only != null && lines[i + 1].isNotBlank() && GLYPH_ITEM.find(lines[i + 1]) == null && GLYPH_ONLY.find(lines[i + 1]) == null) {
            lines[i] = lines[i].trimEnd() + " " + lines[i + 1].trimStart()
            lines.removeAt(i + 1)
        }
        val number = NUMBER_ONLY.find(lines[i])
        if (number != null && lines[i + 1].isNotBlank() && GLYPH_ONLY.find(lines[i + 1]) == null && NUMBER_ONLY.find(lines[i + 1]) == null) {
            lines[i] = number.groupValues[1] + " " + lines[i + 1].trimStart()
            lines.removeAt(i + 1)
        }
        i++
    }
    class Found(val index: Int, val width: Int, val glyph: Char?, val marker: String, val content: String)
    val found = lines.mapIndexedNotNull { index, line ->
        GLYPH_ITEM.find(line)?.let {
            val indent = it.groupValues[1]
            val glyph = line[indent.length]
            val marker = when (glyph) {
                '☐' -> "- [ ]"
                '☑', '☒' -> "- [x]"
                else -> "-"
            }
            Found(index, spaceWidth(indent), glyph, marker, it.groupValues[2].trimEnd())
        } ?: TABBED_ORDERED.find(line)?.let {
            Found(index, spaceWidth(it.groupValues[1]), null, it.groupValues[2], it.groupValues[3].trimEnd())
        }
    }
    if (found.isEmpty()) return lines.joinToString("\n")
    val widths = found.map { it.width }.distinct().sorted()
    // Without any indent to go by (a list copied from this app's conversation),
    // the bullets tell the depth: •, then ◦, then ‣.
    val byGlyph = widths.size == 1 && found.any { it.glyph in DEPTH_GLYPHS.drop(1) }
    fun depth(f: Found) = if (byGlyph) DEPTH_GLYPHS.indexOf(f.glyph).coerceAtLeast(0) else widths.indexOf(f.width)
    // Items nested under a numbered one need 3 columns; under "- ", 2.
    val step = if (found.any { it.glyph == null } || lines.any { parseListItem(it)?.ordered == true }) 3 else 2
    for (f in found) {
        lines[f.index] = " ".repeat(depth(f) * step) + f.marker + " " + f.content
    }
    return lines.joinToString("\n")
}

/** Lines that read as list items, either as Markdown or as bullet characters. */
internal fun countListItems(text: String): Int =
    normalizePastedList(text).split('\n').count { parseListItem(it) != null }

/**
 * The text to paste for a clipboard holding [html] and [plain], when the HTML
 * carries a list the plain text has lost. Null keeps the plain text.
 */
internal fun listTextForPaste(html: String?, plain: String?): String? {
    if (html == null || !html.contains("<li", ignoreCase = true)) return null
    val converted = htmlToListText(html)
    if (converted.isBlank()) return null
    if (plain == null) return converted
    if (countListItems(plain) >= countListItems(converted)) return null
    // Only the list marks and spacing may differ: anything else means the
    // HTML was read differently from what was copied, and plain text is safer.
    if (contentOf(converted) != contentOf(plain)) return null
    return converted
}

private fun contentOf(text: String): String =
    text.filterNot { it.isWhitespace() || it in "-*+.)0123456789[]xX 　" || it in BULLETS }

private val BLOCKS = setOf(
    "p", "div", "section", "article", "header", "footer", "main", "aside", "nav",
    "blockquote", "pre", "figure", "figcaption", "table", "thead", "tbody", "tr",
    "dl", "dt", "dd", "hr", "address", "details", "summary",
    "h1", "h2", "h3", "h4", "h5", "h6",
)
private val SPACED_BLOCKS = setOf("p", "blockquote", "pre", "table", "h1", "h2", "h3", "h4", "h5", "h6")
private val SKIPPED = setOf("head", "style", "script", "title", "template", "noscript")
private val TAG = Regex("""^<\s*(/?)\s*([a-zA-Z][a-zA-Z0-9]*)([^>]*?)(/?)\s*>$""", RegexOption.DOT_MATCHES_ALL)
private val ATTRIBUTE = Regex("""([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*(?:=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+)))?""")

private fun attributes(source: String): Map<String, String> =
    ATTRIBUTE.findAll(source).associate { m ->
        m.groupValues[1].lowercase() to (m.groups[2]?.value ?: m.groups[3]?.value ?: m.groups[4]?.value ?: "")
    }

/**
 * HTML as plain text with its lists as Markdown items, nested by the lists'
 * own nesting (or Google Docs' aria-level / Slack's data-indent).
 */
internal fun htmlToListText(html: String): String {
    val out = StringBuilder()
    var lineHasContent = false
    // What starts the next line: an item's marker, or the indent of its text.
    var pending = ""
    var skipping: String? = null
    var preDepth = 0

    class OpenList(val ordered: Boolean, var next: Int, val depth: Int, val itemBase: Int)
    val lists = mutableListOf<OpenList>()
    // Column where the text of each open item starts, for its later lines.
    val items = mutableListOf<String>()

    fun continuation() = items.lastOrNull() ?: ""
    fun newline() {
        if (lineHasContent) {
            out.append('\n')
            lineHasContent = false
            pending = continuation()
        }
    }
    fun blankLine() {
        newline()
        if (items.isEmpty() && out.isNotEmpty() && !out.endsWith("\n\n")) out.append('\n')
    }
    fun text(raw: String) {
        if (skipping != null) return
        val decoded = decodeEntities(raw)
        val collapsed = if (preDepth > 0) decoded else decoded.replace(Regex("[ \t\r\n\u000C]+"), " ")
        if (preDepth > 0 && collapsed.contains('\n')) {
            val parts = collapsed.split('\n')
            parts.forEachIndexed { index, part ->
                if (index > 0) {
                    out.append('\n')
                    lineHasContent = false
                    pending = continuation()
                }
                if (part.isNotEmpty()) {
                    if (!lineHasContent) out.append(pending)
                    out.append(part)
                    lineHasContent = true
                }
            }
            return
        }
        if (!lineHasContent) {
            val trimmed = collapsed.trimStart()
            if (trimmed.isEmpty()) return
            out.append(pending).append(trimmed)
        } else {
            out.append(collapsed)
        }
        lineHasContent = true
    }

    var index = 0
    while (index < html.length) {
        val lt = html.indexOf('<', index)
        if (lt < 0) {
            text(html.substring(index))
            break
        }
        if (lt > index) text(html.substring(index, lt))
        if (html.startsWith("<!--", lt)) {
            val close = html.indexOf("-->", lt + 4)
            index = if (close < 0) html.length else close + 3
            continue
        }
        val gt = html.indexOf('>', lt)
        if (gt < 0) {
            text(html.substring(lt))
            break
        }
        index = gt + 1
        val match = TAG.find(html.substring(lt, gt + 1)) ?: continue
        val closing = match.groupValues[1].isNotEmpty()
        val name = match.groupValues[2].lowercase()
        if (skipping != null) {
            if (closing && name == skipping) skipping = null
            continue
        }
        if (!closing && name in SKIPPED && match.groupValues[4].isEmpty()) {
            skipping = name
            continue
        }
        val attrs = if (closing) emptyMap() else attributes(match.groupValues[3])
        when (name) {
            "ul", "ol" -> if (closing) {
                newline()
                lists.removeLastOrNull()?.let { list -> while (items.size > list.itemBase) items.removeLast() }
                pending = continuation()
                if (lists.isEmpty()) blankLine()
            } else {
                if (lists.isEmpty()) blankLine() else newline()
                val depth = attrs["data-indent"]?.toIntOrNull()?.plus(1) ?: (lists.size + 1)
                lists += OpenList(name == "ol", attrs["start"]?.toIntOrNull() ?: 1, depth, items.size)
            }
            "li" -> if (closing) {
                newline()
                items.removeLastOrNull()
                pending = continuation()
            } else {
                newline()
                val list = lists.lastOrNull()
                // An item left open (HTML allows leaving out </li>) ends at its sibling.
                if (list != null) while (items.size > list.itemBase) items.removeLast()
                val depth = attrs["aria-level"]?.toIntOrNull() ?: list?.depth ?: 1
                val number = attrs["value"]?.toIntOrNull() ?: list?.next ?: 1
                val marker = if (list?.ordered == true) "$number. " else "- "
                if (list != null) list.next = number + 1
                // Nested items sit under their parent's text: 2 columns under "- ", 3 under "1. ".
                val indent = items.lastOrNull() ?: " ".repeat((depth - 1).coerceAtLeast(0) * 2)
                pending = indent + marker
                items += indent + " ".repeat(marker.length)
            }
            "br" -> if (lineHasContent) newline() else if (items.isEmpty()) {
                out.append('\n')
                pending = continuation()
            }
            "input" -> if (attrs["type"]?.lowercase() == "checkbox" && !lineHasContent) {
                pending += if ("checked" in attrs) "[x] " else "[ ] "
            }
            "td", "th" -> if (closing) text(" ")
            "pre" -> {
                if (closing) preDepth = (preDepth - 1).coerceAtLeast(0) else preDepth++
                blankLine()
            }
            else -> if (name in BLOCKS) {
                if (name in SPACED_BLOCKS) blankLine() else newline()
                if (!closing && name.length == 2 && name[0] == 'h' && name[1].isDigit() && items.isEmpty()) {
                    pending += "#".repeat(name[1].digitToInt()) + " "
                }
            }
        }
    }
    return out.toString()
        .split('\n')
        .joinToString("\n") { it.trimEnd() }
        .replace(Regex("\n{3,}"), "\n\n")
        .trim('\n')
}

private val ENTITY = Regex("""&(#[0-9]{1,7}|#[xX][0-9a-fA-F]{1,6}|[a-zA-Z]{2,8});""")
private val NAMED = mapOf(
    "amp" to "&", "lt" to "<", "gt" to ">", "quot" to "\"", "apos" to "'", "nbsp" to " ",
    "ensp" to " ", "emsp" to " ", "thinsp" to " ", "ndash" to "–", "mdash" to "—", "hellip" to "…",
    "lsquo" to "‘", "rsquo" to "’", "ldquo" to "“", "rdquo" to "”", "bull" to "•", "middot" to "·",
    "copy" to "©", "reg" to "®", "trade" to "™", "times" to "×", "yen" to "¥",
)

private fun decodeEntities(text: String): String = if ('&' !in text) text else ENTITY.replace(text) { m ->
    val body = m.groupValues[1]
    when {
        body.startsWith("#x") || body.startsWith("#X") -> body.substring(2).toIntOrNull(16)?.let(::codePointString)
        body.startsWith("#") -> body.substring(1).toIntOrNull()?.let(::codePointString)
        else -> NAMED[body]
    } ?: m.value
}

private fun codePointString(code: Int): String? = when {
    code == 0xA0 -> " "
    code in 0..0xFFFF -> code.toChar().toString()
    code in 0x10000..0x10FFFF -> {
        val v = code - 0x10000
        charArrayOf((0xD800 + (v shr 10)).toChar(), (0xDC00 + (v and 0x3FF)).toChar()).concatToString()
    }
    else -> null
}
