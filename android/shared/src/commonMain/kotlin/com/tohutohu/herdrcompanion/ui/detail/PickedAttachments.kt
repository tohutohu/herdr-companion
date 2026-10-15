package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.runtime.Composable
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.saveable.listSaver
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.snapshots.SnapshotStateList
import androidx.compose.runtime.toMutableStateList
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive

/**
 * Saves only metadata and picker identifiers, never bytes or a durable URI grant.
 * Android picker read grants keep their original lifetime; a revoked grant is
 * reported by the metadata read or send operation and requires picking again.
 */
@Composable
fun rememberPickedAttachments(owner: String): SnapshotStateList<AttachmentUiState> =
    rememberSaveable(owner, saver = PickedAttachmentsSaver) { mutableStateListOf() }

internal val PickedAttachmentsSaver = listSaver<SnapshotStateList<AttachmentUiState>, String>(
    save = { attachments ->
        listOf(PICKED_ATTACHMENTS_V2) + attachments.flatMap { listOf(it.id, it.name, it.mimeType, it.size?.toString().orEmpty()) }
    },
    restore = { saved ->
        if (saved.firstOrNull() == PICKED_ATTACHMENTS_V2) {
            saved.drop(1).chunked(4).map {
                AttachmentUiState(it[0], it[1], it[2], it[3].toLongOrNull()?.takeIf { size -> size >= 0 })
            }
        } else {
            // State saved before metadata included file sizes used triplets.
            saved.chunked(3).map { AttachmentUiState(it[0], it[1], it[2]) }
        }.toMutableStateList()
    },
)

private const val PICKED_ATTACHMENTS_V2 = "herdr-picked-attachments-v2"

data class PickedAttachmentReadResult(val attachments: List<AttachmentUiState>, val failedCount: Int) {
    val error: String?
        get() = when (failedCount) {
            0 -> null
            1 -> "Could not read an attachment. Select it again or choose another file."
            else -> "Could not read $failedCount attachments. Select them again or choose other files."
        }
}

/** One unavailable provider must not crash the picker callback or lose the other files. */
suspend fun readPickedAttachmentMetadata(
    ids: List<String>,
    read: suspend (String) -> AttachmentUiState,
): PickedAttachmentReadResult {
    val attachments = mutableListOf<AttachmentUiState>()
    var failed = 0
    for (id in ids.distinct()) {
        currentCoroutineContext().ensureActive()
        try {
            attachments += read(id)
        } catch (cancel: CancellationException) {
            throw cancel
        } catch (_: Exception) {
            failed++
        }
    }
    return PickedAttachmentReadResult(attachments, failed)
}
