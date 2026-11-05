package com.tohutohu.herdrcompanion.data.db

import android.app.Application
import android.content.ContentValues
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config
import org.robolectric.annotation.SQLiteMode

@RunWith(RobolectricTestRunner::class)
@Config(application = Application::class, sdk = [35])
@SQLiteMode(SQLiteMode.Mode.NATIVE)
class DatabaseTest {
    @Test
    fun `全旧バージョンから会話とセッションを失わず移行できる`() = runBlocking(Dispatchers.IO) {
        val context = RuntimeEnvironment.getApplication()
        for (version in 1..11) {
            context.deleteDatabase("herdr-mobile.db")
            val schema = javaClass.getResourceAsStream("/com.tohutohu.herdrcompanion.data.db.AppDatabase/$version.json")!!
                .bufferedReader().use { Json.parseToJsonElement(it.readText()).jsonObject.getValue("database").jsonObject }
            context.openOrCreateDatabase("herdr-mobile.db", 0, null).use { old ->
                schema.getValue("entities").jsonArray.forEach { item ->
                    val entity = item.jsonObject
                    val table = entity.getValue("tableName").jsonPrimitive.content
                    old.execSQL(entity.getValue("createSql").jsonPrimitive.content.replace("\${TABLE_NAME}", table))
                    val values = ContentValues()
                    entity.getValue("fields").jsonArray.forEach { field ->
                        val column = field.jsonObject.getValue("columnName").jsonPrimitive.content
                        val affinity = field.jsonObject.getValue("affinity").jsonPrimitive.content
                        if (affinity == "TEXT") values.put(column, when (column) {
                            "id" -> if (table == "sessions") "session-test" else "message-test"
                            "sessionId" -> "session-test"
                            "blocksJson" -> "[]"
                            else -> "preserved-$column"
                        }) else values.put(column, 0)
                    }
                    old.insertOrThrow(table, null, values)
                }
                schema.getValue("setupQueries").jsonArray.forEach { old.execSQL(it.jsonPrimitive.content) }
                old.version = version
            }
            val db = AppDatabase.create(context)
            try {
                assertEquals("migration $version", "preserved-title", db.sessions().get("session-test")!!.title)
                val session = db.sessions().get("session-test")!!
                assertNull("migration $version", session.translation)
                db.sessions().upsert(listOf(session.copy(translation = true)))
                assertEquals(true, db.sessions().get("session-test")!!.translation)
                val messages = db.messages().observe("session-test").first()
                assertEquals("migration $version", listOf("message-test"), messages.map { it.id })
                assertEquals("[]", messages.single().blocksJson)
                assertEquals(12, db.openHelper.readableDatabase.version)
            } finally {
                db.close()
                context.deleteDatabase("herdr-mobile.db")
            }
        }
    }

    @Test
    fun `会話の末尾置換は前半を残して削除と挿入を行う`() = runBlocking(Dispatchers.IO) {
        val context = RuntimeEnvironment.getApplication()
        context.deleteDatabase("herdr-mobile.db")
        val db = AppDatabase.create(context)
        try {
            fun message(id: String, position: Int) = MessageEntity("s", id, position, "user", 0, "[]")
            db.messages().upsert(listOf(message("m1", 0), message("m2-old", 1), message("m3-old", 2)))
            db.messages().replaceFrom("s", 1, listOf(message("m2-new", 1)))
            assertEquals(listOf("m1", "m2-new"), db.messages().observe("s").first().map { it.id })
        } finally {
            db.close()
            context.deleteDatabase("herdr-mobile.db")
        }
    }
    @Test
    fun `版8から移行すると保留中の設定の列は空で始まり保存した値を読み戻せる`() = runBlocking(Dispatchers.IO) {
        val context = RuntimeEnvironment.getApplication()
        context.deleteDatabase("herdr-mobile.db")
        val schema = javaClass.getResourceAsStream("/com.tohutohu.herdrcompanion.data.db.AppDatabase/8.json")!!
            .bufferedReader().use { Json.parseToJsonElement(it.readText()).jsonObject.getValue("database").jsonObject }
        context.openOrCreateDatabase("herdr-mobile.db", 0, null).use { old ->
            schema.getValue("entities").jsonArray.forEach { item ->
                val entity = item.jsonObject
                val table = entity.getValue("tableName").jsonPrimitive.content
                old.execSQL(entity.getValue("createSql").jsonPrimitive.content.replace("\${TABLE_NAME}", table))
            }
            old.execSQL(
                "INSERT INTO sessions (id, provider, providerName, project, status, updatedAt, canSend, listed, lastSyncedAt) " +
                    "VALUES ('claude:s1', 'claude', 'Claude Code', 'p', 'running', 0, 1, 1, 0)",
            )
            schema.getValue("setupQueries").jsonArray.forEach { old.execSQL(it.jsonPrimitive.content) }
            old.version = 8
        }
        val db = AppDatabase.create(context)
        try {
            val migrated = db.sessions().get("claude:s1")!!
            assertEquals("running", migrated.status)
            assertNull(migrated.pendingModel)
            assertNull(migrated.error)
            db.sessions().upsert(
                listOf(migrated.copy(pendingModel = "sonnet", pendingEffort = "high", settingsApply = "next_request", error = "boom")),
            )
            val stored = db.sessions().get("claude:s1")!!
            assertEquals("sonnet", stored.pendingModel)
            assertEquals("high", stored.pendingEffort)
            assertEquals("next_request", stored.settingsApply)
            assertEquals("boom", stored.error)
        } finally {
            db.close()
            context.deleteDatabase("herdr-mobile.db")
        }
    }

    @Test
    fun `版9から移行するとペインと実行先の列が消え他の値は残る`() = runBlocking(Dispatchers.IO) {
        val context = RuntimeEnvironment.getApplication()
        context.deleteDatabase("herdr-mobile.db")
        val schema = javaClass.getResourceAsStream("/com.tohutohu.herdrcompanion.data.db.AppDatabase/9.json")!!
            .bufferedReader().use { Json.parseToJsonElement(it.readText()).jsonObject.getValue("database").jsonObject }
        context.openOrCreateDatabase("herdr-mobile.db", 0, null).use { old ->
            schema.getValue("entities").jsonArray.forEach { item ->
                val entity = item.jsonObject
                val table = entity.getValue("tableName").jsonPrimitive.content
                old.execSQL(entity.getValue("createSql").jsonPrimitive.content.replace("\${TABLE_NAME}", table))
            }
            old.execSQL(
                "INSERT INTO sessions (id, provider, providerName, project, status, updatedAt, paneId, canSend, listed, lastSyncedAt, " +
                    "unread, backend, pendingModel, error) " +
                    "VALUES ('claude:s1', 'claude', 'Claude Code', 'p', 'running', 0, 'w1:p1', 1, 1, 0, 1, 'herdr', 'sonnet', 'boom')",
            )
            old.execSQL(
                "INSERT INTO messages (sessionId, id, position, role, timestamp, blocksJson) VALUES ('claude:s1', 'm1', 0, 'user', 0, '[]')",
            )
            schema.getValue("setupQueries").jsonArray.forEach { old.execSQL(it.jsonPrimitive.content) }
            old.version = 9
        }
        val db = AppDatabase.create(context)
        try {
            val migrated = db.sessions().get("claude:s1")!!
            assertEquals("running", migrated.status)
            assertEquals(true, migrated.unread)
            assertEquals("sonnet", migrated.pendingModel)
            assertEquals("boom", migrated.error)
            assertEquals(listOf("m1"), db.messages().observe("claude:s1").first().map { it.id })
            val columns = db.openHelper.readableDatabase.query("PRAGMA table_info(sessions)").use { cursor ->
                buildList { while (cursor.moveToNext()) add(cursor.getString(cursor.getColumnIndexOrThrow("name"))) }
            }
            assertEquals(emptyList<String>(), columns.filter { it == "paneId" || it == "backend" })
            assertEquals(12, db.openHelper.readableDatabase.version)
        } finally {
            db.close()
            context.deleteDatabase("herdr-mobile.db")
        }
    }
}
