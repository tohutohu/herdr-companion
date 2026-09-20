package com.tohutohu.herdrcompanion.data.db

import android.content.Context
import androidx.room.AutoMigration
import androidx.room.ColumnInfo
import androidx.room.Dao
import androidx.room.Database
import androidx.room.Entity
import androidx.room.Query
import androidx.room.Room
import androidx.room.RoomDatabase
import androidx.room.Transaction
import androidx.room.Upsert
import kotlinx.coroutines.flow.Flow

/** Cached copy of the gateway's session list. */
@Entity(tableName = "sessions", primaryKeys = ["id"])
data class SessionEntity(
    val id: String,
    val provider: String,
    val providerName: String,
    val project: String,
    val title: String?,
    val cwd: String?,
    val status: String,
    val updatedAt: Long,
    val lastMessage: String?,
    val paneId: String?,
    val canSend: Boolean,
    val model: String? = null,
    val effort: String? = null,
    val mode: String? = null,
    /** Context window fill, as the gateway last reported it; null when unknown. */
    val contextUsedTokens: Long? = null,
    val contextWindowTokens: Long? = null,
    val contextUsedPercent: Int? = null,
    /** Session cost in USD; null until a turn has run on a priced model. */
    val costUsd: Double? = null,
    val costEstimated: Boolean? = null,
    @ColumnInfo(defaultValue = "0")
    val archived: Boolean = false,
    /** Whether the gateway still lists it; stale rows are kept for deep links. */
    val listed: Boolean,
    val lastSyncedAt: Long,
    /** Whether a push arrived since the session was last opened. */
    @ColumnInfo(defaultValue = "0")
    val unread: Boolean = false,
)

/** Messages keep their blocks as JSON; they are only rendered, never queried. */
@Entity(tableName = "messages", primaryKeys = ["sessionId", "id"])
data class MessageEntity(
    val sessionId: String,
    val id: String,
    val position: Int,
    val role: String,
    val timestamp: Long,
    val blocksJson: String,
    @ColumnInfo(defaultValue = "0")
    val queued: Boolean = false,
)

@Dao
interface SessionDao {
    @Query("SELECT * FROM sessions WHERE listed = 1 ORDER BY status = 'offline', updatedAt DESC")
    fun observeListed(): Flow<List<SessionEntity>>

    @Query("SELECT * FROM sessions WHERE id = :id")
    fun observe(id: String): Flow<SessionEntity?>

    @Query("SELECT * FROM sessions WHERE id = :id")
    suspend fun get(id: String): SessionEntity?

    @Query("SELECT id FROM sessions WHERE unread = 1")
    suspend fun unreadIds(): List<String>

    @Upsert
    suspend fun upsert(sessions: List<SessionEntity>)

    @Query("UPDATE sessions SET unread = :unread WHERE id = :id")
    suspend fun setUnread(id: String, unread: Boolean)

    @Query("UPDATE sessions SET listed = 0 WHERE id NOT IN (:ids)")
    suspend fun unlistExcept(ids: List<String>)

    @Transaction
    suspend fun replaceListing(sessions: List<SessionEntity>) {
        val unread = unreadIds().toSet()
        upsert(sessions.map { session -> if (session.id in unread) session.copy(unread = true) else session })
        unlistExcept(sessions.map { it.id })
    }

    /** Updates gateway data without allowing a sync to clear a local unread flag. */
    @Transaction
    suspend fun upsertPreservingUnread(session: SessionEntity) {
        val current = get(session.id)
        upsert(listOf(if (current?.unread == true) session.copy(unread = true) else session))
    }

    @Query("DELETE FROM sessions WHERE listed = 0 AND lastSyncedAt < :before")
    suspend fun deleteStale(before: Long)
}

@Dao
interface MessageDao {
    @Query("SELECT * FROM messages WHERE sessionId = :sessionId ORDER BY position")
    fun observe(sessionId: String): Flow<List<MessageEntity>>

    @Query("SELECT id FROM messages WHERE sessionId = :sessionId ORDER BY position DESC LIMIT 1")
    suspend fun lastId(sessionId: String): String?

    @Query("SELECT position FROM messages WHERE sessionId = :sessionId AND id = :id")
    suspend fun positionOf(sessionId: String, id: String): Int?

    @Query("DELETE FROM messages WHERE sessionId = :sessionId")
    suspend fun deleteAll(sessionId: String)

    @Query("DELETE FROM messages WHERE sessionId = :sessionId AND position >= :from")
    suspend fun deleteFrom(sessionId: String, from: Int)

    @Upsert
    suspend fun upsert(messages: List<MessageEntity>)

    @Query("DELETE FROM messages WHERE sessionId NOT IN (SELECT id FROM sessions)")
    suspend fun deleteOrphans()

    /** Replaces the tail of the conversation starting at [from]. */
    @Transaction
    suspend fun replaceFrom(sessionId: String, from: Int, messages: List<MessageEntity>) {
        if (from == 0) deleteAll(sessionId) else deleteFrom(sessionId, from)
        upsert(messages)
    }
}

@Database(
    entities = [SessionEntity::class, MessageEntity::class],
    version = 8,
    exportSchema = true,
    autoMigrations = [
        AutoMigration(from = 1, to = 2),
        AutoMigration(from = 2, to = 3),
        AutoMigration(from = 3, to = 4),
        AutoMigration(from = 4, to = 5),
        AutoMigration(from = 5, to = 6),
        AutoMigration(from = 6, to = 7),
        AutoMigration(from = 7, to = 8),
    ],
)
abstract class AppDatabase : RoomDatabase() {
    abstract fun sessions(): SessionDao
    abstract fun messages(): MessageDao

    companion object {
        fun create(context: Context): AppDatabase =
            Room.databaseBuilder(context, AppDatabase::class.java, "herdr-mobile.db")
                .fallbackToDestructiveMigration(dropAllTables = true)
                .build()
    }
}
