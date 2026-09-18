package com.tohutohu.herdrmobile.data

import android.content.Context
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

/** Folders offered as one-tap shortcuts when starting a new session. */
data class DirectoryShortcuts(val favorites: List<String> = emptyList(), val recents: List<String> = emptyList())

const val MAX_RECENT_DIRECTORIES = 8

/** The directory used by the most recently started session, if any. */
fun lastUsedDirectory(shortcuts: DirectoryShortcuts): String = shortcuts.recents.firstOrNull().orEmpty()

/** Moves [path] to the front, dropping duplicates and anything past [max]. */
fun pushRecent(recents: List<String>, path: String, max: Int = MAX_RECENT_DIRECTORIES): List<String> =
    (listOf(path) + recents.filter { it != path }).take(max)

/** Adds [path] (kept sorted) or removes it if already present. */
fun toggleFavorite(favorites: List<String>, path: String): List<String> =
    if (path in favorites) favorites - path else (favorites + path).sorted()

private val Context.shortcutStore by preferencesDataStore("directory_shortcuts")

class DirectoryShortcutsStore(private val context: Context) {
    private val favoritesKey = stringPreferencesKey("favorites")
    private val recentsKey = stringPreferencesKey("recents")

    val shortcuts: Flow<DirectoryShortcuts> = context.shortcutStore.data.map {
        DirectoryShortcuts(it.list(favoritesKey), it.list(recentsKey))
    }

    suspend fun recordUsed(path: String) = update(recentsKey) { pushRecent(it, path) }

    suspend fun toggleFavorite(path: String) = update(favoritesKey) { toggleFavorite(it, path) }

    suspend fun removeRecent(path: String) = update(recentsKey) { it - path }

    private suspend fun update(key: Preferences.Key<String>, f: (List<String>) -> List<String>) {
        context.shortcutStore.edit { it[key] = f(it.list(key)).joinToString("\n") }
    }

    private fun Preferences.list(key: Preferences.Key<String>): List<String> =
        this[key].orEmpty().split('\n').filter { it.isNotEmpty() }
}
