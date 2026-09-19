package com.tohutohu.herdrmobile.data

/** Folders offered as one-tap shortcuts when starting a new session. */
data class DirectoryShortcuts(val favorites: List<String> = emptyList(), val recents: List<String> = emptyList())

const val MAX_RECENT_DIRECTORIES = 8

fun lastUsedDirectory(shortcuts: DirectoryShortcuts): String = shortcuts.recents.firstOrNull().orEmpty()

fun pushRecent(recents: List<String>, path: String, max: Int = MAX_RECENT_DIRECTORIES): List<String> =
    (listOf(path) + recents.filter { it != path }).take(max)

fun toggleFavorite(favorites: List<String>, path: String): List<String> =
    if (path in favorites) favorites - path else favorites + path

fun moveFavorite(favorites: List<String>, fromIndex: Int, toIndex: Int): List<String> {
    if (fromIndex !in favorites.indices || toIndex !in favorites.indices || fromIndex == toIndex) return favorites
    return favorites.toMutableList().apply { add(toIndex, removeAt(fromIndex)) }
}
