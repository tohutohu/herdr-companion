package com.tohutohu.herdrcompanion.ui.detail

import com.tohutohu.herdrcompanion.data.Message
import com.tohutohu.herdrcompanion.data.api.ReloadFindingDto

/**
 * What a manual reload found. [missed] is a question or approval the app did
 * not show as it should have; the gateway has recorded it for diagnosis.
 */
data class ReloadNotice(
    val text: String,
    val missed: Boolean,
    /** The agent waits on its screen for something the app cannot answer. */
    val openTerminal: Boolean = false,
)

/** The pending interactions [messages] show, as the gateway's reload checks them. */
fun pendingInteractionIds(messages: List<Message>): List<String> =
    messages.flatMap { m -> m.blocks.mapNotNull { b -> b.interaction?.takeIf { it.isPending }?.id } }.distinct()

fun reloadNotice(findings: List<ReloadFindingDto>): ReloadNotice {
    if (findings.isEmpty()) return ReloadNotice("Reloaded. Nothing was missed.", missed = false)
    val text = findings.map { it.kind }.distinct().joinToString("\n") { kind ->
        when (kind) {
            "not_shown" -> "Reload found a question this screen was not showing."
            "stale_cache" -> "Reload found a question the gateway's cached read missed."
            "uncaptured" -> "The agent is waiting, but its question was not captured. Answer it in the terminal."
            else -> findings.first { it.kind == kind }.message.ifEmpty { kind }
        }
    }
    return ReloadNotice(
        text = "$text\nRecorded on the gateway for diagnosis.",
        missed = true,
        openTerminal = findings.any { it.kind == "uncaptured" },
    )
}
