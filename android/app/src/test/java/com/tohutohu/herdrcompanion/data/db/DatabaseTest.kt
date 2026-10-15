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
        for (version in 1..7) {
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
                val messages = db.messages().observe("session-test").first()
                assertEquals("migration $version", listOf("message-test"), messages.map { it.id })
                assertEquals("[]", messages.single().blocksJson)
                assertEquals(8, db.openHelper.readableDatabase.version)
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
}
