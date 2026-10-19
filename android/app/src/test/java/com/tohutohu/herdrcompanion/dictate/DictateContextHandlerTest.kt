package com.tohutohu.herdrcompanion.dictate

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

class DictateContextHandlerTest {
    @get:Rule val folder = TemporaryFolder()

    private val store by lazy { ProjectVocabularyStore(folder.root.resolve("vocabulary.json")) }
    private val fields = DictateFieldRegistry()
    private val handler by lazy { DictateContextHandler(store, fields) }
    private var project: String? = "herdr-android-client"

    init {
        fields.register(TOKEN) { project }
    }

    private fun call(method: String, payload: String, callerAllowed: Boolean = true): JsonObject {
        val reply = handler.handle(method, payload, callerAllowed)!!
        val expectedAction = if (method == DictateContract.GET_CONTEXT) DictateContract.CONTEXT_RESULT else DictateContract.MUTATION_RESULT
        assertEquals(expectedAction, reply.action)
        return Json.parseToJsonElement(reply.payloadJson).jsonObject
    }

    private fun getContext(token: String = TOKEN, callerAllowed: Boolean = true) = call(
        DictateContract.GET_CONTEXT,
        """{"version":1,"requestId":"r1","sessionToken":"$token"}""",
        callerAllowed,
    )

    private fun addVocabulary(term: String, mutationId: String = "m1", contextId: String = "project:herdr-android-client", revision: String? = null) = call(
        DictateContract.ADD_VOCABULARY,
        """{"version":1,"requestId":"r2","mutationId":"$mutationId","sessionToken":"$TOKEN","contextId":"$contextId"${revision?.let { ""","expectedRevision":"$it"""" } ?: ""},"term":"$term"}""",
    )

    private val JsonObject.status get() = this["status"]!!.jsonPrimitive.content

    @Test
    fun `入力欄のプロジェクトをコンテキストとして語彙と置換ルールを返す`() {
        store.addVocabulary("herdr-android-client", "Herdr")
        store.addReplacement("herdr-android-client", "はーだー", "Herdr")
        store.addVocabulary("onnoti-tracker", "OnNoti")

        val result = getContext()

        assertEquals("ok", result.status)
        assertEquals("r1", result["requestId"]!!.jsonPrimitive.content)
        val context = result["context"]!!.jsonObject
        assertEquals("project:herdr-android-client", context["id"]!!.jsonPrimitive.content)
        assertEquals("herdr-android-client", context["displayName"]!!.jsonPrimitive.content)
        assertEquals("2", context["revision"]!!.jsonPrimitive.content)
        assertEquals(listOf("READ_CONTEXT", "ADD_VOCABULARY", "ADD_REPLACEMENT"), result["capabilities"]!!.jsonArray.map { it.jsonPrimitive.content })
        assertEquals(listOf("Herdr"), result["vocabulary"]!!.jsonArray.map { it.jsonPrimitive.content })
        val rule = result["replacements"]!!.jsonArray.single().jsonObject
        assertEquals("はーだー", rule["from"]!!.jsonPrimitive.content)
        assertEquals("Herdr", rule["to"]!!.jsonPrimitive.content)
    }

    @Test
    fun `プロジェクトが分からない入力欄にはno_contextを返す`() {
        project = null
        assertEquals("no_context", getContext().status)
    }

    @Test
    fun `許可されていない呼び出し元や生きていないトークンは拒否する`() {
        assertEquals("denied", getContext(callerAllowed = false).status)
        assertEquals("denied", getContext(token = "unknown").status)
        fields.unregister(TOKEN)
        assertEquals("denied", getContext().status)
    }

    @Test
    fun `バージョンが文字列や1以外の要求には返信しない`() {
        assertNull(handler.handle(DictateContract.GET_CONTEXT, """{"version":"1","requestId":"r","sessionToken":"$TOKEN"}""", true))
        assertNull(handler.handle(DictateContract.GET_CONTEXT, """{"version":2,"requestId":"r","sessionToken":"$TOKEN"}""", true))
        assertNull(handler.handle("other", """{"version":1,"requestId":"r","sessionToken":"$TOKEN"}""", true))
        assertNull(handler.handle(DictateContract.GET_CONTEXT, "not json", true))
    }

    @Test
    fun `語彙の追加は入力欄のプロジェクトに保存され同じmutationIdの再送は登録済みになる`() {
        val added = addVocabulary("Compose", revision = "0")
        assertEquals("ok", added.status)
        assertEquals("m1", added["mutationId"]!!.jsonPrimitive.content)
        assertEquals("1", added["revision"]!!.jsonPrimitive.content)
        assertEquals(listOf("Compose"), store["herdr-android-client"].vocabulary)

        assertEquals("already_exists", addVocabulary("Compose").status)
        assertEquals(1L, store["herdr-android-client"].revision)
    }

    @Test
    fun `古いrevisionや別のコンテキストへの追加はstale_contextになる`() {
        store.addVocabulary("herdr-android-client", "Herdr")
        assertEquals("stale_context", addVocabulary("Compose", revision = "0").status)
        assertEquals("stale_context", addVocabulary("Compose", mutationId = "m2", contextId = "project:onnoti-tracker").status)
        assertEquals(listOf("Herdr"), store["herdr-android-client"].vocabulary)
    }

    @Test
    fun `空や長すぎる語はinvalid_argumentになる`() {
        assertEquals("invalid_argument", addVocabulary("  ").status)
        assertEquals("invalid_argument", addVocabulary("a".repeat(101), mutationId = "m2").status)
        assertTrue(store["herdr-android-client"].vocabulary.isEmpty())
    }

    @Test
    fun `置換ルールを追加できtoは空でもよい`() {
        val result = call(
            DictateContract.ADD_REPLACEMENT,
            """{"version":1,"requestId":"r3","mutationId":"m3","sessionToken":"$TOKEN","contextId":"project:herdr-android-client","from":"えーと","to":""}""",
        )
        assertEquals("ok", result.status)
        assertEquals(listOf("えーと" to ""), store["herdr-android-client"].replacements.map { it.from to it.to })
    }

    @Test
    fun `返信が上限を超えるときは末尾を削ってTruncatedを付ける`() {
        repeat(1000) { store.addVocabulary("herdr-android-client", "%03d".format(it) + "語".repeat(96)) }

        val reply = handler.handle(DictateContract.GET_CONTEXT, """{"version":1,"requestId":"r1","sessionToken":"$TOKEN"}""", true)!!
        val result = Json.parseToJsonElement(reply.payloadJson).jsonObject

        assertTrue(reply.payloadJson.length <= DictateContextHandler.MAX_PAYLOAD_CHARS)
        assertTrue(result["vocabularyTruncated"]!!.jsonPrimitive.content.toBoolean())
        assertEquals("000" + "語".repeat(96), result["vocabulary"]!!.jsonArray.first().jsonPrimitive.content)
    }

    private companion object {
        const val TOKEN = "q8Zr1mT0bXk2wP9s4vYc7A"
    }
}
