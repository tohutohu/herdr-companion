package com.tohutohu.herdrcompanion.ui.settings

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.ExpandLess
import androidx.compose.material.icons.filled.ExpandMore
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.container
import com.tohutohu.herdrcompanion.dictate.ProjectVocabulary
import com.tohutohu.herdrcompanion.dictate.ProjectVocabularyStore
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch

/** What Dictate has registered for each project; Dictate itself can only add. */
@Composable
fun DictationVocabularySection() {
    val container = LocalContext.current.container
    val store = container.projectVocabulary
    // Writes touch the disk; keep them off the main thread and alive past the screen.
    val edit: (ProjectVocabularyStore.() -> Unit) -> Unit = { change ->
        container.scope.launch(Dispatchers.IO) { runCatching { store.change() } }
    }
    val projects by store.projects.collectAsState()
    val filled = projects.filterValues { it.vocabulary.isNotEmpty() || it.replacements.isNotEmpty() }.toSortedMap()

    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text("Dictation words", style = MaterialTheme.typography.titleMedium)
        Text(
            "Words and replacement rules Dictate added from a session's message field. Each project keeps its own, shared by all of its sessions.",
            style = MaterialTheme.typography.bodyMedium,
        )
        if (filled.isEmpty()) {
            Text("Nothing registered yet.", style = MaterialTheme.typography.bodySmall)
        } else {
            filled.forEach { (project, entry) -> ProjectRow(project, entry, edit) }
        }
    }
}

@Composable
private fun ProjectRow(project: String, entry: ProjectVocabulary, edit: (ProjectVocabularyStore.() -> Unit) -> Unit) {
    var expanded by rememberSaveable(project) { mutableStateOf(false) }
    Column {
        Row(
            Modifier.fillMaxWidth().clickable { expanded = !expanded }.padding(vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Column(Modifier.weight(1f)) {
                Text(project, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(
                    "${entry.vocabulary.size} words · ${entry.replacements.size} rules",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Icon(if (expanded) Icons.Default.ExpandLess else Icons.Default.ExpandMore, contentDescription = null)
        }
        if (expanded) {
            entry.vocabulary.forEach { term ->
                EntryRow(term, "Delete $term") { edit { removeVocabulary(project, term) } }
            }
            entry.replacements.forEach { rule ->
                val label = "${rule.from} → ${rule.to.ifEmpty { "(remove)" }}"
                EntryRow(label, "Delete rule ${rule.from}") { edit { removeReplacement(project, rule.id) } }
            }
        }
    }
}

@Composable
private fun EntryRow(text: String, deleteLabel: String, onDelete: () -> Unit) {
    Row(Modifier.fillMaxWidth().padding(start = 16.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(text, Modifier.weight(1f), maxLines = 2, overflow = TextOverflow.Ellipsis)
        IconButton(onClick = onDelete) { Icon(Icons.Default.Delete, contentDescription = deleteLabel) }
    }
}
