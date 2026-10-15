package com.tohutohu.herdrcompanion.push

import java.io.File
import java.io.FileOutputStream
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import java.util.UUID
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

/** WorkManager stores only [id]; potentially large reply text stays in private files. */
@Serializable
internal data class ReplyPayload(
    val id: String = UUID.randomUUID().toString(),
    val connectionId: String,
    val sessionId: String,
    val status: String,
    val title: String,
    val body: String,
    val reply: String,
    val createdAt: Long = System.currentTimeMillis(),
    val attempted: Boolean = false,
    val safeToRetry: Boolean = false,
    val terminal: Boolean = false,
)

/** Atomic replacements survive process death without changing Room's cache schema. */
internal class ReplyPayloadStore(private val directory: File) {
    private val json = Json { ignoreUnknownKeys = true }

    fun create(payload: ReplyPayload): ReplyPayload = synchronized(lock) {
        val last = all().maxOfOrNull { it.createdAt } ?: Long.MIN_VALUE
        payload.copy(createdAt = maxOf(payload.createdAt, last + 1)).also(::save)
    }

    fun save(payload: ReplyPayload) = synchronized(lock) {
        check(directory.isDirectory || directory.mkdirs()) { "Could not save the reply" }
        val target = file(payload.id)
        val temporary = File.createTempFile(".reply-", ".tmp", directory)
        try {
            temporary.setReadable(false, false)
            temporary.setWritable(false, false)
            check(temporary.setReadable(true, true) && temporary.setWritable(true, true))
            FileOutputStream(temporary).use { stream ->
                stream.write(json.encodeToString(payload).toByteArray(Charsets.UTF_8))
                stream.fd.sync()
            }
            Files.move(temporary.toPath(), target.toPath(), StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING)
        } finally {
            temporary.delete()
        }
    }

    fun load(id: String): ReplyPayload? = synchronized(lock) {
        val target = file(id)
        if (!target.exists()) null else json.decodeFromString<ReplyPayload>(target.readText())
    }

    fun delete(id: String) = synchronized(lock) { file(id).delete(); Unit }

    /** Restore queue metadata without retaining every large reply in memory. */
    fun pendingIds(connectionId: String): List<String> = synchronized(lock) {
        all().filter { !it.terminal && it.connectionId == connectionId }
            .map { it.id to it.createdAt }.sortedBy { it.second }.map { it.first }.toList()
    }

    /** Old connections cannot send; retain terminal failures briefly for local recovery. */
    fun cleanup(connectionId: String, now: Long = System.currentTimeMillis()) = synchronized(lock) {
        all().filter { it.connectionId != connectionId || (it.terminal && now - it.createdAt > TERMINAL_RETENTION_MS) }
            .forEach { delete(it.id) }
        // A process killed while writing may leave a temporary, never a queued payload.
        directory.listFiles()?.filter { it.name.endsWith(".tmp") && now - it.lastModified() > TERMINAL_RETENTION_MS }
            ?.forEach { it.delete() }
    }

    private fun all(): Sequence<ReplyPayload> = directory.listFiles().orEmpty().asSequence()
        .filter { it.name.endsWith(".json") }
        .mapNotNull { file -> runCatching { json.decodeFromString<ReplyPayload>(file.readText()) }.getOrNull() }

    private fun file(id: String): File {
        require(ID.matches(id)) { "Invalid reply ID" }
        return File(directory, "$id.json")
    }

    private companion object {
        val lock = Any()
        val ID = Regex("[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}")
        const val TERMINAL_RETENTION_MS = 30L * 24 * 60 * 60 * 1_000
    }
}

/** UI failures must finish successfully at the scheduler level so later replies run. */
internal enum class ReplyDecision { SEND, RETRY, FINISH, UNCONFIRMED }

internal fun resumedReplyDecision(payload: ReplyPayload, gatewaySupportsReceipts: Boolean): ReplyDecision = when {
    payload.terminal -> ReplyDecision.FINISH
    payload.attempted && (!payload.safeToRetry || !gatewaySupportsReceipts) -> ReplyDecision.UNCONFIRMED
    else -> ReplyDecision.SEND
}

internal fun failedReplyDecision(payload: ReplyPayload, code: Int?, attempt: Int): ReplyDecision = when {
    payload.attempted && !payload.safeToRetry -> ReplyDecision.UNCONFIRMED
    code == 409 -> ReplyDecision.UNCONFIRMED
    code != null && code in 400..499 -> ReplyDecision.FINISH
    attempt >= 3 -> if (payload.attempted) ReplyDecision.UNCONFIRMED else ReplyDecision.FINISH
    else -> ReplyDecision.RETRY
}
