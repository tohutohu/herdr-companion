package com.tohutohu.herdrcompanion.ui.newsession

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyItemScope
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.History
import androidx.compose.material.icons.filled.Star
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.data.DirectoryShortcuts
import com.tohutohu.herdrcompanion.ui.ExpandingContent

/**
 * Favorite folders, then recently used ones, as a horizontal chip row.
 * Recents that are also favorites are shown once, as favorites.
 */
@Composable
fun DirectoryShortcutsRow(
    shortcuts: DirectoryShortcuts,
    currentPath: String,
    enabled: Boolean,
    onOpen: (String) -> Unit,
    onReorderFavorites: (List<String>) -> Unit,
    modifier: Modifier = Modifier,
) {
    val recents = shortcuts.recents.filter { it !in shortcuts.favorites }
    // The row appears with the first shortcut instead of pushing the browser down.
    ExpandingContent(value = shortcuts.takeIf { it.favorites.isNotEmpty() || recents.isNotEmpty() }, modifier = modifier) {
        ShortcutChips(
            favorites = it.favorites,
            recents = it.recents.filter { r -> r !in it.favorites },
            currentPath = currentPath,
            enabled = enabled,
            onOpen = onOpen,
            onReorderFavorites = onReorderFavorites,
        )
    }
}

@Composable
private fun ShortcutChips(
    favorites: List<String>,
    recents: List<String>,
    currentPath: String,
    enabled: Boolean,
    onOpen: (String) -> Unit,
    onReorderFavorites: (List<String>) -> Unit,
) {
    val labels = shortLabels(favorites + recents)

    @Composable
    fun LazyItemScope.Shortcut(path: String, icon: ImageVector, description: String, modifier: Modifier = Modifier) {
        FilterChip(
            selected = path == currentPath,
            enabled = enabled,
            onClick = { onOpen(path) },
            label = { Text(labels.getValue(path)) },
            leadingIcon = { Icon(icon, contentDescription = description, Modifier.size(FilterChipDefaults.IconSize)) },
            modifier = modifier.animateItem(),
        )
    }

    ReorderableChipRow(
        rowItems = favorites,
        itemKey = { "fav:$it" },
        onMove = onReorderFavorites,
        modifier = Modifier.fillMaxWidth(),
        contentPadding = PaddingValues(horizontal = 16.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        itemContent = { path, reorderModifier ->
            Shortcut(path, Icons.Default.Star, "Favorite", reorderModifier)
        },
        afterItems = {
            // Starring moves a chip between the groups; let it travel.
            items(recents, key = { "recent:$it" }) { path ->
                Shortcut(path, Icons.Default.History, "Recent")
            }
        },
    )
}

/**
 * Folder names for chips: the last path segment, or "parent/name" when two
 * paths share the same last segment.
 */
fun shortLabels(paths: List<String>): Map<String, String> {
    fun segments(p: String) = p.trimEnd('/').split('/').filter { it.isNotEmpty() }
    val nameCounts = paths.distinct().groupingBy { segments(it).lastOrNull() }.eachCount()
    return paths.associateWith { p ->
        val segs = segments(p)
        when {
            segs.isEmpty() -> "/"
            (nameCounts[segs.last()] ?: 0) > 1 && segs.size > 1 -> segs.takeLast(2).joinToString("/")
            else -> segs.last()
        }
    }
}
