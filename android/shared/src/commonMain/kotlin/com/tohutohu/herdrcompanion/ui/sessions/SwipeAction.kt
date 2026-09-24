package com.tohutohu.herdrcompanion.ui.sessions

/** What a horizontal swipe on a session row does. */
enum class SwipeAction(val label: String) {
    ARCHIVE("Archive"),
    STOP_AND_ARCHIVE("Stop and archive"),
    UNARCHIVE("Unarchive"),
}

/** Archived rows swipe back into the list; listed rows swipe into the archive. */
fun swipeActionFor(s: SessionRef): SwipeAction = when {
    s.archived -> SwipeAction.UNARCHIVE
    s.live -> SwipeAction.STOP_AND_ARCHIVE
    else -> SwipeAction.ARCHIVE
}

/** How a batch of session actions ended, for the toast / snackbar. */
data class BatchOutcome(
    val total: Int,
    val failed: Int,
    val failure: String? = null,
    val warning: String? = null,
) {
    /** [done] describes a single success, [verb] a batch ("archived"). */
    fun message(done: String, verb: String): String = when {
        total == 1 -> failure?.let { "Failed: $it" } ?: warning ?: done
        failed == 0 -> listOfNotNull("$total sessions $verb", warning).joinToString("\n")
        failed == total -> "Failed: $failure"
        else -> "${total - failed} $verb, $failed failed: $failure"
    }
}

/**
 * Whether the snackbar offers to undo. Stopping an agent cannot be undone by
 * unarchiving, so only sessions that were already offline qualify.
 */
fun undoableTargets(succeeded: List<SessionRef>, stopped: Boolean): List<SessionRef> =
    if (stopped) emptyList() else succeeded
