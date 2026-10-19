package com.tohutohu.herdrcompanion.dictate

import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.put
import kotlinx.serialization.json.putJsonArray
import kotlinx.serialization.json.putJsonObject

data class DictateReply(val action: String, val payloadJson: String)

/**
 * Dictate protocol v1 on top of [ProjectVocabularyStore]: one context per
 * Herdr project, so every session of a project shares its words and rules.
 */
class DictateContextHandler(
    private val store: ProjectVocabularyStore,
    private val fields: DictateFieldRegistry,
) {
    /** Recent mutation results, so Dictate's re-sent "register" stays idempotent. */
    private val doneMutations = object : LinkedHashMap<String, Unit>() {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, Unit>?) = size > 200
    }

    /** Returns null for requests that are not ours or cannot be answered at all. */
    fun handle(method: String, payloadJson: String?, callerAllowed: Boolean): DictateReply? {
        if (method != DictateContract.GET_CONTEXT && method != DictateContract.ADD_VOCABULARY &&
            method != DictateContract.ADD_REPLACEMENT
        ) return null
        val req = payloadJson?.let { runCatching { Json.parseToJsonElement(it).jsonObject }.getOrNull() } ?: return null
        val version = req["version"] as? JsonPrimitive
        if (version == null || version.isString || version.intOrNull != DictateContract.VERSION) return null
        val requestId = req.string("requestId") ?: return null
        val token = req.string("sessionToken")
        val allowed = callerAllowed && fields.isLive(token)
        val payload = if (method == DictateContract.GET_CONTEXT) {
            contextResult(requestId, token, allowed)
        } else {
            mutationResult(method, req, requestId, token, allowed) ?: return null
        }
        val action = if (method == DictateContract.GET_CONTEXT) DictateContract.CONTEXT_RESULT else DictateContract.MUTATION_RESULT
        return DictateReply(action, payload.toString())
    }

    private fun contextResult(requestId: String, token: String?, allowed: Boolean): JsonObject {
        val project = fields.projectFor(token)?.takeIf { contextId(it).length <= MAX_ID_LENGTH }
        if (!allowed || project == null) {
            return buildJsonObject {
                put("version", DictateContract.VERSION)
                put("requestId", requestId)
                put("status", if (allowed) "no_context" else "denied")
            }
        }
        val entry = store[project]
        var vocabularyLimit = MAX_VOCABULARY
        var replacementsLimit = MAX_REPLACEMENTS
        while (true) {
            val result = contextResult(requestId, project, entry, vocabularyLimit, replacementsLimit)
            // Dictate drops the whole reply past this size; shed the tail until it fits.
            if (result.toString().length <= MAX_PAYLOAD_CHARS) return result
            vocabularyLimit = minOf(vocabularyLimit, entry.vocabulary.size) * 9 / 10
            replacementsLimit = minOf(replacementsLimit, entry.replacements.size) * 9 / 10
        }
    }

    private fun contextResult(
        requestId: String,
        project: String,
        entry: ProjectVocabulary,
        vocabularyLimit: Int,
        replacementsLimit: Int,
    ): JsonObject = buildJsonObject {
        val vocabulary = entry.vocabulary.take(vocabularyLimit)
        val replacements = entry.replacements.take(replacementsLimit)
        put("version", DictateContract.VERSION)
        put("requestId", requestId)
        put("status", "ok")
        putJsonObject("context") {
            put("id", contextId(project))
            put("displayName", project.take(MAX_DISPLAY_NAME_LENGTH))
            put("revision", entry.revision.toString())
        }
        putJsonArray("capabilities") {
            add(JsonPrimitive("READ_CONTEXT"))
            add(JsonPrimitive("ADD_VOCABULARY"))
            add(JsonPrimitive("ADD_REPLACEMENT"))
        }
        putJsonArray("vocabulary") { vocabulary.forEach { add(JsonPrimitive(it)) } }
        putJsonArray("replacements") {
            replacements.forEach { rule ->
                add(buildJsonObject {
                    put("id", rule.id)
                    put("from", rule.from)
                    put("to", rule.to)
                })
            }
        }
        put("vocabularyTruncated", vocabulary.size < entry.vocabulary.size)
        put("replacementsTruncated", replacements.size < entry.replacements.size)
    }

    private fun mutationResult(method: String, req: JsonObject, requestId: String, token: String?, allowed: Boolean): JsonObject? {
        val mutationId = req.string("mutationId") ?: return null
        fun reply(status: String, revision: Long? = null) = buildJsonObject {
            put("version", DictateContract.VERSION)
            put("requestId", requestId)
            put("mutationId", mutationId)
            put("status", status)
            if (revision != null) put("revision", revision.toString())
        }
        if (!allowed) return reply("denied")
        val project = fields.projectFor(token) ?: return reply("stale_context")
        val revision = store[project].revision
        if (synchronized(doneMutations) { mutationId in doneMutations }) return reply("already_exists", revision)
        val expected = req.string("expectedRevision")
        if (req.string("contextId") != contextId(project) || (expected != null && expected != revision.toString())) {
            return reply("stale_context", revision)
        }
        val result = if (method == DictateContract.ADD_VOCABULARY) {
            val term = req.string("term")?.trim()
            if (term == null || !valid(term, 1, MAX_TERM_LENGTH)) return reply("invalid_argument", revision)
            store.addVocabulary(project, term)
        } else {
            val from = req.string("from")?.trim()
            val to = req.string("to")?.trim()
            if (from == null || to == null || !valid(from, 1, MAX_REPLACEMENT_TEXT_LENGTH) || !valid(to, 0, MAX_REPLACEMENT_TEXT_LENGTH)) {
                return reply("invalid_argument", revision)
            }
            store.addReplacement(project, from, to)
        }
        synchronized(doneMutations) { doneMutations[mutationId] = Unit }
        val status = if (result == AddResult.OK) "ok" else "already_exists"
        return reply(status, store[project].revision)
    }

    private fun JsonObject.string(key: String): String? =
        (this[key] as? JsonPrimitive)?.takeIf { it.isString }?.contentOrNull

    private fun valid(text: String, min: Int, max: Int) = text.length in min..max && text.none { it.isISOControl() }

    companion object {
        const val MAX_PAYLOAD_CHARS = 100_000
        const val MAX_VOCABULARY = 1000
        const val MAX_REPLACEMENTS = 500
        const val MAX_TERM_LENGTH = 100
        const val MAX_REPLACEMENT_TEXT_LENGTH = 200
        const val MAX_ID_LENGTH = 256
        const val MAX_DISPLAY_NAME_LENGTH = 100

        fun contextId(project: String) = "project:$project"
    }
}
