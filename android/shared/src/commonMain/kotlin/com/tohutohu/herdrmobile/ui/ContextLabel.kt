package com.tohutohu.herdrmobile.ui

import java.util.Locale

/**
 * Token counts as the agents themselves show them: "154k", "1M". Below 1000
 * the exact number is kept, which only happens for a brand new session.
 */
fun tokenCount(tokens: Long): String = when {
    tokens >= 1_000_000 && tokens % 1_000_000 == 0L -> "${tokens / 1_000_000}M"
    tokens >= 1_000_000 -> String.format(Locale.ROOT, "%.1fM", tokens / 1_000_000.0)
    tokens >= 1_000 -> "${tokens / 1_000}k"
    else -> tokens.toString()
}

/** "154k / 1M", the filled part of the context window against its size. */
fun contextFill(usedTokens: Long?, windowTokens: Long?): String? {
    if (usedTokens == null || windowTokens == null || windowTokens <= 0) return null
    return "${tokenCount(usedTokens)} / ${tokenCount(windowTokens)}"
}

/** "ctx 15%", the compact form for lists. Null when the agent reported nothing. */
fun contextLabel(usedPercent: Int?): String? = usedPercent?.let { "ctx $it%" }
