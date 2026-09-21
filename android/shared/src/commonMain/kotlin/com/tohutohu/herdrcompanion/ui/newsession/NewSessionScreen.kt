package com.tohutohu.herdrcompanion.ui.newsession

import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.SizeTransform
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.ArrowUpward
import androidx.compose.material.icons.filled.CreateNewFolder
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.Star
import androidx.compose.material.icons.filled.StarBorder
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.ui.ExpandingContent
import com.tohutohu.herdrcompanion.ui.SwapContent

/** Pure Compose rendering for choosing an agent and working directory. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun NewSessionScreen(
    state: NewSessionUiState,
    onAction: (NewSessionAction) -> Unit,
    topContent: @Composable () -> Unit = {},
    initialPromptFocusRequester: FocusRequester? = null,
) {
    val pending = state.pendingStart
    if (pending != null) {
        AlertDialog(
            onDismissRequest = { onAction(NewSessionAction.ChangeFolder) },
            title = { Text("Check the working folder") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    Text("This request may belong to a different project, based on this folder's files and available session history.")
                    Text(pending.request.cwd, fontFamily = FontFamily.Monospace)
                    Text(pending.request.prompt, maxLines = 6, overflow = TextOverflow.Ellipsis)
                }
            },
            confirmButton = {
                TextButton(onClick = { onAction(NewSessionAction.StartAnyway) }) { Text("Start anyway") }
            },
            dismissButton = {
                TextButton(onClick = { onAction(NewSessionAction.ChangeFolder) }) { Text("Change folder") }
            },
        )
    }

    if (state.showMkdir) {
        var name by remember { mutableStateOf("") }
        AlertDialog(
            onDismissRequest = { onAction(NewSessionAction.CancelMkdir) },
            title = { Text("New folder") },
            text = {
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it },
                    singleLine = true,
                    label = { Text("Folder name") },
                    supportingText = { Text("in ${state.path}") },
                )
            },
            confirmButton = {
                TextButton(
                    enabled = name.isNotBlank(),
                    onClick = { onAction(NewSessionAction.CreateDirectory(name.trim())) },
                ) { Text("Create") }
            },
            dismissButton = { TextButton(onClick = { onAction(NewSessionAction.CancelMkdir) }) { Text("Cancel") } },
        )
    }

    if (state.showPicker) {
        AgentPickerDialog(
            provider = state.provider,
            model = state.model,
            effort = state.effort,
            mode = state.mode,
            catalog = state.catalog,
            modelsError = state.modelsError,
            favorite = state.favorite,
            onProvider = { onAction(NewSessionAction.SetProvider(it)) },
            onModel = { onAction(NewSessionAction.SetModel(it)) },
            onEffort = { onAction(NewSessionAction.SetEffort(it)) },
            onMode = { onAction(NewSessionAction.SetMode(it)) },
            onToggleFavorite = { onAction(NewSessionAction.ToggleFavorite) },
            onDismiss = { onAction(NewSessionAction.DismissPicker) },
        )
    }

    Scaffold(
        topBar = {
            TopAppBar(
                navigationIcon = {
                    IconButton(onClick = { onAction(NewSessionAction.Back) }) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
                title = { Text("New session") },
            )
        },
        bottomBar = {
            Surface(tonalElevation = 3.dp) {
                Column(
                    Modifier.navigationBarsPadding().imePadding().padding(12.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    AgentPresetsRow(
                        presets = state.savedPresets,
                        current = state.currentPreset,
                        onSelect = { onAction(NewSessionAction.SelectPreset(it)) },
                        onReorder = { onAction(NewSessionAction.ReorderPresets(it)) },
                        onCustomize = { onAction(NewSessionAction.CustomizeAgent) },
                    )
                    OutlinedTextField(
                        value = state.prompt,
                        onValueChange = { onAction(NewSessionAction.SetPrompt(it)) },
                        enabled = !state.starting && !state.checking,
                        label = { Text("First prompt (optional)") },
                        maxLines = 4,
                        modifier = if (initialPromptFocusRequester != null) {
                            Modifier.fillMaxWidth().focusRequester(initialPromptFocusRequester)
                        } else {
                            Modifier.fillMaxWidth()
                        },
                    )
                    Button(
                        enabled = state.path.isNotEmpty() && !state.starting && !state.checking && !state.loading && state.pendingStart == null,
                        modifier = Modifier.fillMaxWidth(),
                        onClick = { onAction(NewSessionAction.Start) },
                    ) {
                        val busyLabel = when {
                            state.starting -> "Starting…"
                            state.checking -> "Checking folder…"
                            else -> null
                        }
                        AnimatedContent(
                            targetState = busyLabel ?: "Start ${providerName(state.provider)} in ${state.path.substringAfterLast('/').ifEmpty { "…" }}",
                            transitionSpec = { fadeIn(tween(160)) togetherWith fadeOut(tween(100)) using SizeTransform(clip = false) },
                            contentAlignment = Alignment.Center,
                            label = "startButton",
                        ) { label ->
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                if (busyLabel != null) CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                                Text(if (busyLabel != null) "  $label" else label, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            }
                        }
                    }
                }
            }
        },
    ) { padding ->
        Column(Modifier.padding(padding).fillMaxSize()) {
            topContent()
            DirectoryShortcutsRow(
                shortcuts = state.shortcuts,
                currentPath = state.path,
                enabled = !state.loading && !state.checking && !state.starting,
                onOpen = { onAction(NewSessionAction.OpenDirectory(it)) },
                onReorderFavorites = { onAction(NewSessionAction.ReorderFavoriteDirectories(it)) },
                modifier = Modifier.padding(top = 8.dp),
            )
            Row(
                verticalAlignment = Alignment.CenterVertically,
                modifier = Modifier.padding(start = 8.dp, end = 8.dp),
            ) {
                IconButton(
                    enabled = state.listing?.parent != null && !state.loading && !state.checking && !state.starting,
                    onClick = { onAction(NewSessionAction.OpenParent) },
                ) { Icon(Icons.Default.ArrowUpward, contentDescription = "Parent folder") }
                AnimatedContent(
                    targetState = state.path,
                    transitionSpec = { fadeIn(tween(160)) togetherWith fadeOut(tween(100)) using SizeTransform(clip = false) },
                    contentAlignment = Alignment.CenterStart,
                    modifier = Modifier.weight(1f),
                    label = "path",
                ) { shown ->
                    Text(
                        shown.ifEmpty { "Workspace roots" },
                        fontFamily = FontFamily.Monospace,
                        style = MaterialTheme.typography.bodySmall,
                        maxLines = 2,
                        overflow = TextOverflow.StartEllipsis,
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
                val favoriteDir = state.path in state.shortcuts.favorites
                IconButton(
                    enabled = state.path.isNotEmpty(),
                    onClick = { onAction(NewSessionAction.ToggleFavoriteDirectory) },
                ) {
                    SwapContent(favoriteDir) { starred ->
                        Icon(
                            if (starred) Icons.Default.Star else Icons.Default.StarBorder,
                            contentDescription = if (starred) "Remove from favorites" else "Add to favorites",
                        )
                    }
                }
                IconButton(
                    enabled = state.path.isNotEmpty() && !state.loading && !state.checking && !state.starting,
                    onClick = { onAction(NewSessionAction.ShowMkdir) },
                ) { Icon(Icons.Default.CreateNewFolder, contentDescription = "New folder") }
            }
            ExpandingContent(value = state.error) {
                Text(it, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(horizontal = 16.dp))
            }
            HorizontalDivider()
            ExpandingContent(value = Unit.takeIf { state.loading && state.listing == null }) {
                CircularProgressIndicator(Modifier.padding(24.dp))
            }
            val shownPath = state.listing?.path ?: state.path
            AnimatedContent(
                targetState = shownPath,
                transitionSpec = {
                    val deeper = targetState.length >= initialState.length
                    (slideInHorizontally(tween(260)) { if (deeper) it / 6 else -it / 6 } + fadeIn(tween(200)))
                        .togetherWith(slideOutHorizontally(tween(260)) { if (deeper) -it / 6 else it / 6 } + fadeOut(tween(120)))
                },
                label = "listing",
            ) { current ->
                val entries = if (current == shownPath) state.listing?.entries.orEmpty() else emptyList()
                LazyColumn(Modifier.fillMaxSize()) {
                    if (entries.isEmpty() && !state.loading && current == shownPath) {
                        item(key = "empty") { Text("No subfolders", modifier = Modifier.animateItem().padding(16.dp)) }
                    }
                    items(entries, key = { it.path }) { dir ->
                        Row(
                            verticalAlignment = Alignment.CenterVertically,
                            modifier = Modifier
                                .animateItem()
                                .fillMaxWidth()
                                .clickable(enabled = !state.loading && !state.checking && !state.starting) {
                                    onAction(NewSessionAction.OpenDirectory(dir.path))
                                }
                                .padding(horizontal = 16.dp, vertical = 12.dp),
                        ) {
                            Icon(Icons.Default.Folder, contentDescription = null, tint = MaterialTheme.colorScheme.primary)
                            Text(dir.name, modifier = Modifier.padding(start = 12.dp))
                        }
                    }
                }
            }
        }
    }
}
