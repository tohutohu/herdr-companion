package com.tohutohu.herdrcompanion.data

import androidx.room.withTransaction
import com.tohutohu.herdrcompanion.data.api.BlockDto
import com.tohutohu.herdrcompanion.data.api.GatewayApi
import com.tohutohu.herdrcompanion.data.api.InteractionResponseDto
import com.tohutohu.herdrcompanion.data.api.MessageDto
import com.tohutohu.herdrcompanion.data.api.ReloadFindingDto
import com.tohutohu.herdrcompanion.data.api.SessionDto
import com.tohutohu.herdrcompanion.data.api.SessionsResponse
import com.tohutohu.herdrcompanion.data.db.AppDatabase
import com.tohutohu.herdrcompanion.data.db.MessageEntity
import com.tohutohu.herdrcompanion.data.db.SessionEntity
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.builtins.ListSerializer
import java.time.Instant
import java.util.concurrent.ConcurrentHashMap

/**
 * Room is the UI's read source; this class pulls from the gateway and
 * writes into Room.
 */
class SessionRepository(
    private val db: AppDatabase,
    private val api: GatewayApi,
) {
    private val blocksSerializer = ListSerializer(BlockDto.serializer())
    private val listingLock = Mutex()
    private val messageLocks = ConcurrentHashMap<String, Mutex>()

    fun observeSessions(): Flow<List<SessionEntity>> = db.sessions().observeListed()

    fun observeSession(id: String): Flow<SessionEntity?> = db.sessions().observe(id)

    /**
     * Read state lives in Herdr: the gateway reports it with each session and
     * marking it seen focuses the pane there. The cached row is cleared first
     * so the list does not wait for the next sync; a failed call leaves the
     * next sync to bring the unread state back.
     */
    suspend fun markSeenIfUnread(sessionId: String) {
        if (db.sessions().get(sessionId)?.unread != true) return
        db.sessions().setUnread(sessionId, unread = false)
        api.markSeen(sessionId)
    }

    fun observeMessages(sessionId: String): Flow<List<Message>> =
        db.messages().observe(sessionId).map { rows ->
            rows.map { row ->
                val blocks = runCatching { GatewayApi.json.decodeFromString(blocksSerializer, row.blocksJson) }
                    .getOrElse { listOf(BlockDto(type = "text", text = "(unreadable cached message)")) }
                Message(row.id, row.role, row.timestamp, blocks, row.queued)
            }
        }

    suspend fun refreshSessions() = listingLock.withLock {
        val now = System.currentTimeMillis()
        val sessions = api.sessions()
        db.sessions().replaceListing(sessions.map { it.toEntity(now, listed = true) })
        db.sessions().deleteStale(now - STALE_MS)
        db.messages().deleteOrphans()
    }

    /**
     * Fetches messages for a session. Incremental fetches ask for everything
     * from the last cached non-queued message on; the gateway includes that anchor so
     * in-progress changes to it are picked up. An incremental fetch the
     * gateway reports unchanged leaves the cache as it is.
     */
    suspend fun refreshMessages(sessionId: String, full: Boolean = false): Unit = messageLock(sessionId).withLock {
        val anchor = if (full) null else db.messages().lastAnchorId(sessionId)
        val snapshot = if (anchor == null) {
            null
        } else {
            // Unchanged since the last fetch: a pushed report is already cached.
            api.messagesIfChanged(sessionId, after = anchor) ?: return@withLock db.messages().deletePushed(sessionId)
        }
        val resp = snapshot?.response ?: api.messages(sessionId)
        val now = System.currentTimeMillis()
        // An archived session is only listed while it runs.
        val s = resp.session
        db.withTransaction {
            db.sessions().upsert(listOf(s.toEntity(now, listed = !s.archived || s.isLive)))
            val anchorPos = anchor?.let { db.messages().positionOf(sessionId, it) }
            val incremental = anchor != null && anchorPos != null && resp.messages.firstOrNull()?.id == anchor
            val start = if (incremental) requireNotNull(anchorPos) else 0
            val rows = resp.messages.mapIndexed { i, m -> m.toEntity(sessionId, start + i) }
            db.messages().deletePushed(sessionId)
            db.messages().replaceFrom(sessionId, start, rows)
        }
        snapshot?.acknowledge()
    }

    /**
     * Shows the report a push carried until the conversation can be fetched,
     * at the end of what is cached. Its `push:` id keeps it from serving as
     * the incremental anchor, and the next fetch replaces it. It takes no
     * session lock: a fetch in flight would hold up the push handler, and
     * either order of the two transactions ends with the fetched rows.
     */
    suspend fun showPushedReport(push: PushedReport) = db.withTransaction {
        val sessionId = push.sessionId
        val existing = db.sessions().get(sessionId)
        val reportedAt = parseTime(push.report.timestamp)
        db.sessions().upsert(
            listOf(
                existing?.copy(status = push.status, updatedAt = maxOf(existing.updatedAt, reportedAt))
                    ?: SessionEntity(
                        id = sessionId,
                        provider = push.provider,
                        providerName = push.providerName,
                        project = push.project,
                        title = null,
                        cwd = null,
                        status = push.status,
                        updatedAt = reportedAt,
                        lastMessage = null,
                        paneId = null,
                        canSend = push.canSend,
                        listed = true,
                        lastSyncedAt = System.currentTimeMillis(),
                    ),
            ),
        )
        db.messages().deletePushed(sessionId)
        if (db.messages().positionOf(sessionId, push.report.id) != null) return@withTransaction
        val position = (db.messages().lastPosition(sessionId) ?: -1) + 1
        db.messages().upsert(listOf(push.report.copy(id = PUSHED_PREFIX + push.report.id).toEntity(sessionId, position)))
    }

    /**
     * Reads the whole conversation again past the gateway's caches and
     * replaces the cached one. [shown] are the pending interactions on screen;
     * returns what the gateway found missed.
     */
    suspend fun reloadMessages(sessionId: String, shown: List<String>): List<ReloadFindingDto> = messageLock(sessionId).withLock {
        val resp = api.reload(sessionId, shown)
        val s = resp.session
        db.withTransaction {
            db.sessions().upsert(listOf(s.toEntity(System.currentTimeMillis(), listed = !s.archived || s.isLive)))
            db.messages().replaceFrom(sessionId, 0, resp.messages.mapIndexed { i, m -> m.toEntity(sessionId, i) })
        }
        resp.findings
    }

    private fun messageLock(sessionId: String): Mutex = messageLocks.getOrPut(sessionId) { Mutex() }

    suspend fun archivedSessions(offset: Int, limit: Int): SessionsResponse = api.archivedSessions(offset, limit)

    suspend fun archive(sessionId: String) = archive(listOf(sessionId))

    /** Returns the gateway's warnings, e.g. a worktree kept because it has changes. */
    suspend fun archive(sessionIds: List<String>): List<String> {
        val now = System.currentTimeMillis()
        val archived = api.archive(sessionIds)
        archived.forEach { s ->
            db.sessions().upsert(listOf(s.toEntity(now, listed = false)))
        }
        return archived.mapNotNull { it.warning }
    }

    suspend fun unarchive(sessionId: String) {
        val s = api.unarchive(sessionId)
        db.sessions().upsert(listOf(s.toEntity(System.currentTimeMillis(), listed = true)))
    }

    /** Keeps the pane id available when resuming stops at a startup dialog. */
    suspend fun resume(sessionId: String, trust: Boolean = true): com.tohutohu.herdrcompanion.data.api.StartSessionResponse {
        val res = api.resume(sessionId, trust)
        runCatching { refreshSessions() }
        return res
    }

    suspend fun send(sessionId: String, text: String, uploads: List<String>) =
        api.sendMessage(sessionId, text, uploads)

    suspend fun respond(sessionId: String, response: InteractionResponseDto) =
        api.respond(sessionId, response)

    suspend fun cycleMode(sessionId: String) = api.cycleMode(sessionId)

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
        private const val PUSHED_PREFIX = "push:"

        fun parseTime(s: String): Long = runCatching { Instant.parse(s).toEpochMilli() }.getOrDefault(0L)
    }
}

/** What a push says about a session, enough to show its last report. */
data class PushedReport(
    val sessionId: String,
    val status: String,
    val provider: String,
    val providerName: String,
    val project: String,
    val canSend: Boolean,
    val report: MessageDto,
)

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
    unread = unread,
)
