package com.tohutohu.herdrmobile.data

import com.tohutohu.herdrmobile.data.api.BlockDto
import com.tohutohu.herdrmobile.data.api.GatewayApi
import com.tohutohu.herdrmobile.data.api.InteractionResponseDto
import com.tohutohu.herdrmobile.data.api.MessageDto
import com.tohutohu.herdrmobile.data.api.SessionDto
import com.tohutohu.herdrmobile.data.db.AppDatabase
import com.tohutohu.herdrmobile.data.db.MessageEntity
import com.tohutohu.herdrmobile.data.db.SessionEntity
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map
import kotlinx.serialization.builtins.ListSerializer
import java.time.Instant

/**
 * Room is the UI's read source; this class pulls from the gateway and
 * writes into Room.
 */
class SessionRepository(
    private val db: AppDatabase,
    private val api: GatewayApi,
) {
    private val blocksSerializer = ListSerializer(BlockDto.serializer())

    fun observeSessions(): Flow<List<SessionEntity>> = db.sessions().observeListed()

    fun observeSession(id: String): Flow<SessionEntity?> = db.sessions().observe(id)

    fun observeMessages(sessionId: String): Flow<List<Message>> =
        db.messages().observe(sessionId).map { rows ->
            rows.map { row ->
                val blocks = runCatching { GatewayApi.json.decodeFromString(blocksSerializer, row.blocksJson) }
                    .getOrElse { listOf(BlockDto(type = "text", text = "(unreadable cached message)")) }
                Message(row.id, row.role, row.timestamp, blocks, row.queued)
            }
        }

    suspend fun refreshSessions() {
        val now = System.currentTimeMillis()
        val sessions = api.sessions()
        db.sessions().replaceListing(sessions.map { it.toEntity(now, listed = true) })
        db.sessions().deleteStale(now - STALE_MS)
        db.messages().deleteOrphans()
    }

    /**
     * Fetches messages for a session. Incremental fetches ask for everything
     * from the last cached message on; the gateway includes that anchor so
     * in-progress changes to it are picked up.
     */
    suspend fun refreshMessages(sessionId: String, full: Boolean = false) {
        val anchor = if (full) null else db.messages().lastId(sessionId)
        val resp = api.messages(sessionId, after = anchor)
        val now = System.currentTimeMillis()
        // An archived session is only listed while it runs.
        val s = resp.session
        db.sessions().upsert(listOf(s.toEntity(now, listed = !s.archived || s.isLive)))

        val anchorPos = anchor?.let { db.messages().positionOf(sessionId, it) }
        val incremental = anchor != null && anchorPos != null && resp.messages.firstOrNull()?.id == anchor
        val start = if (incremental) anchorPos!! else 0
        val rows = resp.messages.mapIndexed { i, m -> m.toEntity(sessionId, start + i) }
        db.messages().replaceFrom(sessionId, start, rows)
    }

    suspend fun archivedSessions(): List<SessionDto> = api.archivedSessions()

    suspend fun archive(sessionId: String) {
        val s = api.archive(sessionId)
        db.sessions().upsert(listOf(s.toEntity(System.currentTimeMillis(), listed = false)))
    }

    suspend fun unarchive(sessionId: String) {
        val s = api.unarchive(sessionId)
        db.sessions().upsert(listOf(s.toEntity(System.currentTimeMillis(), listed = true)))
    }

    /** Keeps the pane id available when resuming stops at a startup dialog. */
    suspend fun resume(sessionId: String, trust: Boolean = true): com.tohutohu.herdrmobile.data.api.StartSessionResponse {
        val res = api.resume(sessionId, trust)
        runCatching { refreshSessions() }
        return res
    }

    suspend fun send(sessionId: String, text: String, uploads: List<String>) =
        api.sendMessage(sessionId, text, uploads)

    suspend fun respond(sessionId: String, response: InteractionResponseDto) =
        api.respond(sessionId, response)

    private fun MessageDto.toEntity(sessionId: String, position: Int) = MessageEntity(
        sessionId = sessionId,
        id = id,
        position = position,
        role = role,
        timestamp = parseTime(timestamp),
        blocksJson = GatewayApi.json.encodeToString(blocksSerializer, blocks),
        queued = queued,
    )

    companion object {
        private const val STALE_MS = 14L * 24 * 60 * 60 * 1000

        fun parseTime(s: String): Long = runCatching { Instant.parse(s).toEpochMilli() }.getOrDefault(0L)
    }
}

fun SessionDto.toEntity(now: Long, listed: Boolean) = SessionEntity(
    id = id,
    provider = provider,
    providerName = providerName,
    project = project,
    title = title,
    cwd = cwd,
    status = status,
    updatedAt = SessionRepository.parseTime(updatedAt),
    lastMessage = lastMessage,
    paneId = paneId,
    canSend = canSend,
    model = model,
    effort = effort,
    mode = mode,
    contextUsedTokens = context?.usedTokens,
    contextWindowTokens = context?.windowTokens,
    contextUsedPercent = context?.usedPercent,
    costUsd = cost?.usd,
    costEstimated = cost?.estimated,
    archived = archived,
    listed = listed,
    lastSyncedAt = now,
)
