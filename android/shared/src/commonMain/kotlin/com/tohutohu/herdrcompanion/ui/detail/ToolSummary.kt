package com.tohutohu.herdrcompanion.ui.detail

private val TOOL_CALL_PREFIXES = listOf(
    "Read ", "Edit ", "MultiEdit ", "Write ", "NotebookEdit ", "Glob ", "Grep ",
    "WebFetch ", "WebSearch ", "Agent: ", "▸ ",
)

/**
 * Whether an assistant text block is a tool call rendered by the gateway
 * (Claude transcripts inline them as text) rather than prose.
 */
fun isToolCallText(text: String): Boolean {
    val lines = text.lines()
    // Skill calls include the full args in following lines so they can expand
    // without clipping the invocation. The first-line marker remains the same.
    if (lines.firstOrNull()?.startsWith("▸ ") == true) return true
    // Bash: "$ command", optionally preceded by a one-line description.
    if (lines[0].startsWith("$ ") || lines.getOrNull(1)?.startsWith("$ ") == true) return true
    return lines.size == 1 && TOOL_CALL_PREFIXES.any { lines[0].startsWith(it) }
}

/** One line shown while a tool block is collapsed. */
fun toolSummary(text: String): String {
    val lines = text.trimEnd().lines()
    val first = lines.first().trim()
    return if (lines.size > 1) "$first  (${lines.size} lines)" else first
}
