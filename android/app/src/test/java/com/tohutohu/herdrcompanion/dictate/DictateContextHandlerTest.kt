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
    private var project: String? = PROJECT

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

    /** [fields] is the request's own JSON fields, after the common ones. */
    private fun mutate(method: String, fields: String, mutationId: String = "m1", contextId: String = "project:$PROJECT", revision: String? = null) = call(
        method,
        """{"version":1,"requestId":"r2","mutationId":"$mutationId","sessionToken":"$TOKEN","contextId":"$contextId"${revision?.let { ""","expectedRevision":"$it"""" } ?: ""},$fields}""",
    )

    private fun addVocabulary(term: String, mutationId: String = "m1", contextId: String = "project:$PROJECT", revision: String? = null) =
        mutate(DictateContract.ADD_VOCABULARY, """"term":"$term"""", mutationId, contextId, revision)

    private val JsonObject.status get() = this["status"]!!.jsonPrimitive.content

    private val rules get() = store[PROJECT].replacements.map { it.from to it.to }

    @Test
    fun `入力欄のプロジェクトをコンテキストとして語彙と置換ルールを返す`() {
        store.addVocabulary(PROJECT, "Herdr", listOf("はーだー"))
        store.addVocabulary("onnoti-tracker", "OnNoti")

        val result = getContext()

        assertEquals("ok", result.status)
        assertEquals("r1", result["requestId"]!!.jsonPrimitive.content)
        val context = result["context"]!!.jsonObject
        assertEquals("project:$PROJECT", context["id"]!!.jsonPrimitive.content)
        assertEquals(PROJECT, context["displayName"]!!.jsonPrimitive.content)
        assertEquals("1", context["revision"]!!.jsonPrimitive.content)
        assertEquals(
            listOf("READ_CONTEXT", "ADD_VOCABULARY", "ADD_READINGS", "DELETE_VOCABULARY", "DELETE_REPLACEMENT"),
            result["capabilities"]!!.jsonArray.map { it.jsonPrimitive.content },
        )
        assertEquals(listOf("Herdr"), result["vocabulary"]!!.jsonArray.map { it.jsonPrimitive.content })
        val rule = result["replacements"]!!.jsonArray.single().jsonObject
        assertEquals(store[PROJECT].replacements.single().id, rule["id"]!!.jsonPrimitive.content)
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
        assertEquals("denied", addVocabulary("Compose").status)
    }

    @Test
    fun `バージョンが文字列や1以外の要求と廃止された要求には返信しない`() {
        assertNull(handler.handle(DictateContract.GET_CONTEXT, """{"version":"1","requestId":"r","sessionToken":"$TOKEN"}""", true))
        assertNull(handler.handle(DictateContract.GET_CONTEXT, """{"version":2,"requestId":"r","sessionToken":"$TOKEN"}""", true))
        assertNull(handler.handle("${DictateContract.BASE}.ADD_REPLACEMENT", """{"version":1,"requestId":"r","sessionToken":"$TOKEN"}""", true))
        assertNull(handler.handle(DictateContract.GET_CONTEXT, "not json", true))
    }

    @Test
    fun `読み付きの語を追加すると語彙と読みのルールが入力欄のプロジェクトに保存される`() {
        val added = mutate(
            DictateContract.ADD_VOCABULARY,
            """"term":"OnNoti Tracker","readings":["オンノティトラッカー"," オンノティ トラッカー ","OnNoti Tracker","オンノティトラッカー"]""",
            revision = "0",
        )
        assertEquals("ok", added.status)
        assertEquals("m1", added["mutationId"]!!.jsonPrimitive.content)
        assertEquals("1", added["revision"]!!.jsonPrimitive.content)
        assertEquals(listOf("OnNoti Tracker"), store[PROJECT].vocabulary)
        assertEquals(listOf("オンノティ トラッカー" to "OnNoti Tracker", "オンノティトラッカー" to "OnNoti Tracker"), rules)
    }

    @Test
    fun `同じmutationIdの再送はいまの状態で答える`() {
        assertEquals("ok", addVocabulary("Compose").status)
        assertEquals("already_exists", addVocabulary("Compose").status)

        val delete = { mutate(DictateContract.DELETE_VOCABULARY, """"term":"Compose"""", mutationId = "m2") }
        assertEquals("ok", delete().status)
        assertEquals("not_found", delete().status)
        assertEquals(2L, store[PROJECT].revision)
    }

    @Test
    fun `古いrevisionや別のコンテキストへの変更はstale_contextになる`() {
        store.addVocabulary(PROJECT, "Herdr")
        assertEquals("stale_context", addVocabulary("Compose", revision = "0").status)
        assertEquals("stale_context", addVocabulary("Compose", mutationId = "m2", contextId = "project:onnoti-tracker").status)
        assertEquals(
            "stale_context",
            mutate(DictateContract.DELETE_VOCABULARY, """"term":"Herdr"""", mutationId = "m3", revision = "0").status,
        )
        assertEquals(listOf("Herdr"), store[PROJECT].vocabulary)
    }

    @Test
    fun `空や長すぎる語と多すぎる読みはinvalid_argumentになる`() {
        assertEquals("invalid_argument", addVocabulary("  ").status)
        assertEquals("invalid_argument", addVocabulary("a".repeat(101), mutationId = "m2").status)
        val readings = (1..21).joinToString(",") { "\"よみ$it\"" }
        assertEquals("invalid_argument", mutate(DictateContract.ADD_VOCABULARY, """"term":"語","readings":[$readings]""", mutationId = "m3").status)
        assertEquals("invalid_argument", mutate(DictateContract.ADD_VOCABULARY, """"term":"語","readings":"よみ"""", mutationId = "m4").status)
        assertTrue(store[PROJECT].vocabulary.isEmpty())
        assertTrue(rules.isEmpty())
    }

    @Test
    fun `語彙を削除しても置換ルールは残り無い語はnot_foundになる`() {
        store.addVocabulary(PROJECT, "Kotlin", listOf("ことりん"))
        assertEquals("ok", mutate(DictateContract.DELETE_VOCABULARY, """"term":"Kotlin"""").status)
        assertEquals("not_found", mutate(DictateContract.DELETE_VOCABULARY, """"term":"Kotlin"""", mutationId = "m2").status)
        assertTrue(store[PROJECT].vocabulary.isEmpty())
        assertEquals(listOf("ことりん" to "Kotlin"), rules)
    }

    @Test
    fun `置換ルールはreplacementIdかfromとtoで削除できる`() {
        store.addVocabulary(PROJECT, "Kotlin", listOf("ことりん", "コトリン"))
        val id = store[PROJECT].replacements.first { it.from == "ことりん" }.id

        val byId = mutate(DictateContract.DELETE_REPLACEMENT, """"replacementId":"$id","from":"ことりん","to":"Kotlin"""")
        val byText = mutate(DictateContract.DELETE_REPLACEMENT, """"from":"コトリン","to":"Kotlin"""", mutationId = "m2")
        val missing = mutate(DictateContract.DELETE_REPLACEMENT, """"from":"コトリン","to":"Kotlin"""", mutationId = "m3")

        assertEquals("ok", byId.status)
        assertEquals("ok", byText.status)
        assertEquals("not_found", missing.status)
        assertTrue(rules.isEmpty())
        assertEquals(listOf("Kotlin"), store[PROJECT].vocabulary)
    }

    @Test
    fun `返信が上限を超えるときは末尾を削ってTruncatedを付ける`() {
        repeat(1000) { store.addVocabulary(PROJECT, "%03d".format(it) + "語".repeat(96)) }

        val reply = handler.handle(DictateContract.GET_CONTEXT, """{"version":1,"requestId":"r1","sessionToken":"$TOKEN"}""", true)!!
        val result = Json.parseToJsonElement(reply.payloadJson).jsonObject

        assertTrue(reply.payloadJson.length <= DictateContextHandler.MAX_PAYLOAD_CHARS)
        assertTrue(result["vocabularyTruncated"]!!.jsonPrimitive.content.toBoolean())
        assertEquals("000" + "語".repeat(96), result["vocabulary"]!!.jsonArray.first().jsonPrimitive.content)
    }

    private companion object {
        const val TOKEN = "q8Zr1mT0bXk2wP9s4vYc7A"
        const val PROJECT = "herdr-android-client"
    }
}
