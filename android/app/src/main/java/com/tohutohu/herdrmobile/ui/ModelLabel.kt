package com.tohutohu.herdrmobile.ui

private val claudeModelId = Regex("""^claude-([a-z]+)-(\d+)(?:-(\d{1,2}))?(?:-\d{8})?(\[1m])?$""")

/**
 * Short label for a provider model id: "claude-fable-5-1" → "Fable 5.1",
 * "claude-haiku-4-5-20251001" → "Haiku 4.5". Other ids are shown as-is.
 */
fun modelLabel(model: String): String {
    val m = claudeModelId.matchEntire(model) ?: return model
    val (family, major, minor, longContext) = m.destructured
    val version = if (minor.isEmpty()) major else "$major.$minor"
    val suffix = if (longContext.isEmpty()) "" else " (1M)"
    return family.replaceFirstChar { it.uppercase() } + " " + version + suffix
}

/**
 * One-line summary of how the agent runs, e.g. "Opus 5 (high) · Plan".
 * Returns null when nothing is known.
 */
fun agentSettingsLabel(model: String?, effort: String?, mode: String?): String? {
    val m = model?.takeIf { it.isNotBlank() }?.let(::modelLabel)
    val e = effort?.takeIf { it.isNotBlank() }
    val head = when {
        m != null && e != null -> "$m ($e)"
        m != null -> m
        e != null -> "effort $e"
        else -> null
    }
    return listOfNotNull(head, mode?.takeIf { it.isNotBlank() }).joinToString(" · ").ifEmpty { null }
}
