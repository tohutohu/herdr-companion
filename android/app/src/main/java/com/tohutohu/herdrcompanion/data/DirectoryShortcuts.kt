package com.tohutohu.herdrcompanion.data

import android.content.Context
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map

private val Context.shortcutStore by preferencesDataStore("directory_shortcuts")

class DirectoryShortcutsStore(private val context: Context) {
    private val favoritesKey = stringPreferencesKey("favorites")
    private val recentsKey = stringPreferencesKey("recents")

    val shortcuts: Flow<DirectoryShortcuts> = context.shortcutStore.data.map {
        DirectoryShortcuts(it.list(favoritesKey), it.list(recentsKey))
    }

    suspend fun recordUsed(path: String) = update(recentsKey) { pushRecent(it, path) }

    /**
     * The favorites are the gateway's ([FavoriteDirectoriesSync]); this store
     * keeps the last list it gave, so New Session shows them at once.
     */
    val favoritesCache = object : FavoriteDirectoriesSync.Cache {
        override suspend fun read(): List<String> = context.shortcutStore.data.first().list(favoritesKey)
        override suspend fun write(paths: List<String>) {
            update(favoritesKey) { paths }
        }
    }

    private suspend fun update(key: Preferences.Key<String>, f: (List<String>) -> List<String>) {
        context.shortcutStore.edit { it[key] = f(it.list(key)).joinToString("\n") }
    }

    private fun Preferences.list(key: Preferences.Key<String>): List<String> =
        this[key].orEmpty().split('\n').filter { it.isNotEmpty() }
}
