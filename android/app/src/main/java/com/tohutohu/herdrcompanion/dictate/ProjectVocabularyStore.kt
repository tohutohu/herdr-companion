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

enum class AddResult { OK, ALREADY_EXISTS }

/**
 * Per-project vocabulary and replacement rules that Dictate reads and adds to.
 * Kept in its own file, not Room, so a cache reset never drops what the user
 * taught it. Reads come from Binder threads, so the snapshot lives in a
 * StateFlow and writes are serialized.
 */
class ProjectVocabularyStore(private val file: File) {
    private val lock = Any()
    private val state = MutableStateFlow(load())

    val projects: StateFlow<Map<String, ProjectVocabulary>> = state.asStateFlow()

    operator fun get(project: String): ProjectVocabulary = state.value[project] ?: ProjectVocabulary()

    fun addVocabulary(project: String, term: String): AddResult = mutate(project) { current ->
        if (current.vocabulary.any { it.equals(term, ignoreCase = true) }) null
        else current.copy(vocabulary = current.vocabulary + term)
    }

    /** A rule for an existing `from` replaces its `to` rather than adding a second one. */
    fun addReplacement(project: String, from: String, to: String): AddResult = mutate(project) { current ->
        val existing = current.replacements.indexOfFirst { it.from.equals(from, ignoreCase = true) }
        when {
            existing >= 0 && current.replacements[existing].to == to -> null
            existing >= 0 -> current.copy(
                replacements = current.replacements.toMutableList().also { it[existing] = it[existing].copy(to = to) },
            )
            else -> {
                val rule = Replacement(UUID.randomUUID().toString(), from, to)
                val at = current.replacements.indexOfFirst { it.from.length < from.length }
                    .takeIf { it >= 0 } ?: current.replacements.size
                current.copy(replacements = current.replacements.toMutableList().also { it.add(at, rule) })
            }
        }
    }

    fun removeVocabulary(project: String, term: String) {
        mutate(project) { current -> current.copy(vocabulary = current.vocabulary - term).takeIf { it != current } }
    }

    fun removeReplacement(project: String, id: String) {
        mutate(project) { current ->
            current.copy(replacements = current.replacements.filterNot { it.id == id }).takeIf { it != current }
        }
    }

    /** [change] returns null when nothing changes. */
    private fun mutate(project: String, change: (ProjectVocabulary) -> ProjectVocabulary?): AddResult = synchronized(lock) {
        val all = state.value
        val current = all[project] ?: ProjectVocabulary()
        val next = change(current) ?: return AddResult.ALREADY_EXISTS
        // An emptied project keeps its entry so its revision never goes back.
        val nextAll = all + (project to next.copy(revision = current.revision + 1))
        save(nextAll)
        state.value = nextAll
        AddResult.OK
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
