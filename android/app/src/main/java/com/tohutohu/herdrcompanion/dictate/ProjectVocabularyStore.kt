package com.tohutohu.herdrcompanion.dictate

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.io.File
import java.util.UUID

/** Dictation hints for one Herdr project (workspace). */
@Serializable
data class ProjectVocabulary(
    val vocabulary: List<String> = emptyList(),
    /** Applied in order, so longer `from`s are kept ahead of the shorter ones they contain. */
    val replacements: List<Replacement> = emptyList(),
    /** Bumped on every change; Dictate sends it back to detect a stale view. */
    val revision: Long = 0,
)

@Serializable
data class Replacement(val id: String, val from: String, val to: String)

/**
 * Per-project vocabulary and replacement rules that Dictate reads and edits.
 * Kept in its own file, not Room, so a cache reset never drops what the user
 * taught it. Reads come from Binder threads, so the snapshot lives in a
 * StateFlow and writes are serialized.
 */
class ProjectVocabularyStore(private val file: File) {
    private val lock = Any()
    private val state = MutableStateFlow(load())

    val projects: StateFlow<Map<String, ProjectVocabulary>> = state.asStateFlow()

    operator fun get(project: String): ProjectVocabulary = state.value[project] ?: ProjectVocabulary()

    /**
     * Adds [term] and a `reading → term` rule for each of [readings] in one
     * write. A reading that already maps to another word now maps to this
     * one, as in Dictate's own vocabulary. Returns whether anything changed.
     */
    fun addVocabulary(project: String, term: String, readings: List<String> = emptyList()): Boolean = mutate(project) { current ->
        val vocabulary = if (current.vocabulary.any { it.equals(term, ignoreCase = true) }) current.vocabulary else current.vocabulary + term
        val replacements = readings.fold(current.replacements) { rules, reading -> rules.withRule(reading, term) }
        ProjectVocabulary(vocabulary, replacements, current.revision)
    }

    /** Rules that replace with [term] stay; Dictate deletes those one by one. */
    fun deleteVocabulary(project: String, term: String): Boolean = mutate(project) { current ->
        current.copy(vocabulary = current.vocabulary - term)
    }

    /** Finds the rule by [id] when Dictate has one, otherwise by its `from` and `to`. */
    fun deleteReplacement(project: String, id: String?, from: String, to: String): Boolean = mutate(project) { current ->
        val index = if (id != null) {
            current.replacements.indexOfFirst { it.id == id }
        } else {
            current.replacements.indexOfFirst { it.from == from && it.to == to }
        }
        if (index < 0) current else current.copy(replacements = current.replacements.filterIndexed { i, _ -> i != index })
    }

    private fun List<Replacement>.withRule(from: String, to: String): List<Replacement> {
        val existing = indexOfFirst { it.from.equals(from, ignoreCase = true) }
        return when {
            existing >= 0 && this[existing].to == to -> this
            existing >= 0 -> toMutableList().also { it[existing] = it[existing].copy(to = to) }
            else -> {
                val at = indexOfFirst { it.from.length < from.length }.takeIf { it >= 0 } ?: size
                toMutableList().also { it.add(at, Replacement(UUID.randomUUID().toString(), from, to)) }
            }
        }
    }

    /** Returns false, writing nothing, when [change] leaves the project as it was. */
    private fun mutate(project: String, change: (ProjectVocabulary) -> ProjectVocabulary): Boolean = synchronized(lock) {
        val all = state.value
        val current = all[project] ?: ProjectVocabulary()
        val next = change(current)
        if (next == current) return false
        // An emptied project keeps its entry so its revision never goes back.
        val nextAll = all + (project to next.copy(revision = current.revision + 1))
        save(nextAll)
        state.value = nextAll
        true
    }

    private fun load(): Map<String, ProjectVocabulary> = runCatching {
        if (file.isFile) json.decodeFromString<Stored>(file.readText()).projects else emptyMap()
    }.getOrElse { emptyMap() }

    private fun save(projects: Map<String, ProjectVocabulary>) {
        file.parentFile?.mkdirs()
        val tmp = File(file.parentFile, "${file.name}.tmp")
        tmp.writeText(json.encodeToString(Stored(projects)))
        if (!tmp.renameTo(file)) {
            tmp.delete()
            error("could not save ${file.name}")
        }
    }

    @Serializable
    private data class Stored(val projects: Map<String, ProjectVocabulary> = emptyMap())

    private companion object {
        val json = Json { ignoreUnknownKeys = true }
    }
}
