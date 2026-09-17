package com.tohutohu.herdrmobile.ui.newsession

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyRow
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
import com.tohutohu.herdrmobile.data.DirectoryShortcuts

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
    modifier: Modifier = Modifier,
) {
    val recents = shortcuts.recents.filter { it !in shortcuts.favorites }
    if (shortcuts.favorites.isEmpty() && recents.isEmpty()) return
    val labels = shortLabels(shortcuts.favorites + recents)

    @Composable
    fun Shortcut(path: String, icon: ImageVector, description: String) {
        FilterChip(
            selected = path == currentPath,
            enabled = enabled,
            onClick = { onOpen(path) },
            label = { Text(labels.getValue(path)) },
            leadingIcon = { Icon(icon, contentDescription = description, Modifier.size(FilterChipDefaults.IconSize)) },
        )
    }

    LazyRow(
        modifier = modifier.fillMaxWidth(),
        contentPadding = PaddingValues(horizontal = 16.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        items(shortcuts.favorites, key = { "fav:$it" }) { Shortcut(it, Icons.Default.Star, "Favorite") }
        items(recents, key = { "recent:$it" }) { Shortcut(it, Icons.Default.History, "Recent") }
    }
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
