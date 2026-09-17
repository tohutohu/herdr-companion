package com.tohutohu.herdrmobile.ui.newsession

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
import androidx.compose.material3.Checkbox
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
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrmobile.container
import com.tohutohu.herdrmobile.data.AgentPreset
import com.tohutohu.herdrmobile.data.DirectoryShortcuts
import com.tohutohu.herdrmobile.data.api.DirListingDto
import com.tohutohu.herdrmobile.data.api.ModelsResponse
import com.tohutohu.herdrmobile.data.api.StartSessionRequest
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/**
 * Pick how the agent runs (a favorite agent / model / effort combination, or
 * any other one from the full pickers) and a working directory (browse or
 * create one under the gateway's workspace roots), then start the agent in a
 * new Herdr workspace.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun NewSessionScreen(
    onBack: () -> Unit,
    onStarted: (sessionId: String?, warning: String?) -> Unit,
) {
    val context = LocalContext.current
    val api = context.container.api
    val repo = context.container.repository
    val shortcutStore = context.container.directoryShortcuts
    val presetStore = context.container.agentPresets
    val shortcuts by shortcutStore.shortcuts.collectAsState(initial = DirectoryShortcuts())
    // Null until the store has been read; restoring waits for it.
    val presets by presetStore.presets.collectAsState(initial = null)
    val scope = rememberCoroutineScope()

    var provider by rememberSaveable { mutableStateOf("claude") }
    var path by rememberSaveable { mutableStateOf("") } // "" = list of roots
    var listing by remember { mutableStateOf<DirListingDto?>(null) }
    var loading by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var prompt by rememberSaveable { mutableStateOf("") }
    var trust by rememberSaveable { mutableStateOf(true) }
    var starting by remember { mutableStateOf(false) }
    var checking by remember { mutableStateOf(false) }
    var pendingStart by remember { mutableStateOf<Pair<StartSessionRequest, AgentPreset>?>(null) }
    var showMkdir by remember { mutableStateOf(false) }
    var showPicker by remember { mutableStateOf(false) }
    // "" = the agent's default model / effort.
    var model by rememberSaveable { mutableStateOf("") }
    var effort by rememberSaveable { mutableStateOf("") }
    var catalog by remember { mutableStateOf(ModelsResponse()) }
    var modelsError by remember { mutableStateOf<String?>(null) }
    var restored by rememberSaveable { mutableStateOf(false) }

    val saved = presets?.presets.orEmpty()
    val current = withKnownNames(
        agentPreset(provider, model, effort, catalog),
        saved + listOfNotNull(presets?.lastUsed),
    )
    val favorite = saved.any { it.key == current.key }

    suspend fun start(request: StartSessionRequest, preset: AgentPreset) {
        starting = true
        error = null
        try {
            val res = api.startSession(request)
            runCatching { shortcutStore.recordUsed(request.cwd) }
            runCatching { presetStore.recordUsed(preset) }
            runCatching { repo.refreshSessions() }
            onStarted(res.sessionId, res.warning)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            error = "Start failed: ${e.message}"
        } finally {
            starting = false
        }
    }

    pendingStart?.let { (request, preset) ->
        AlertDialog(
            onDismissRequest = { pendingStart = null },
            title = { Text("Check the working folder") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    Text("This request may belong to a different project, based on this folder's files and available session history.")
                    Text(request.cwd, fontFamily = FontFamily.Monospace)
                    Text(request.prompt, maxLines = 6, overflow = TextOverflow.Ellipsis)
                }
            },
            confirmButton = {
                TextButton(onClick = {
                    pendingStart = null
                    scope.launch { start(request, preset) }
                }) { Text("Start anyway") }
            },
            dismissButton = {
                TextButton(onClick = { pendingStart = null }) { Text("Change folder") }
            },
        )
    }

    suspend fun load(p: String) {
        loading = true
        try {
            val l = api.directories(p.ifEmpty { null })
            listing = l
            path = l.path
            error = null
            // A single root: open it directly.
            if (l.path.isEmpty() && l.entries.size == 1) {
                load(l.entries.first().path)
            }
        } catch (e: Exception) {
            error = e.message
        } finally {
            loading = false
        }
    }

    LaunchedEffect(Unit) { load(path) }

    // Start where the last session left off, once.
    LaunchedEffect(presets) {
        val loaded = presets ?: return@LaunchedEffect
        if (restored) return@LaunchedEffect
        restored = true
        loaded.lastUsed?.let {
            provider = it.provider
            model = it.model
            effort = it.effort
        }
    }

    LaunchedEffect(provider) {
        catalog = ModelsResponse()
        modelsError = null
        try {
            catalog = api.models(provider)
            if (model.isNotEmpty() && catalog.models.none { it.id == model }) model = ""
            if (effort.isNotEmpty() && effortsFor(catalog, model).none { it.id == effort }) effort = ""
        } catch (e: Exception) {
            modelsError = e.message
        }
    }

    if (showMkdir) {
        var name by remember { mutableStateOf("") }
        AlertDialog(
            onDismissRequest = { showMkdir = false },
            title = { Text("New folder") },
            text = {
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it },
                    singleLine = true,
                    label = { Text("Folder name") },
                    supportingText = { Text("in $path") },
                )
            },
            confirmButton = {
                TextButton(
                    enabled = name.isNotBlank(),
                    onClick = {
                        showMkdir = false
                        scope.launch {
                            try {
                                load(api.createDirectory(path, name.trim()))
                            } catch (e: Exception) {
                                error = "Could not create folder: ${e.message}"
                            }
                        }
                    },
                ) { Text("Create") }
            },
            dismissButton = { TextButton(onClick = { showMkdir = false }) { Text("Cancel") } },
        )
    }

    if (showPicker) {
        AgentPickerDialog(
            provider = provider,
            model = model,
            effort = effort,
            catalog = catalog,
            modelsError = modelsError,
            favorite = favorite,
            onProvider = { provider = it },
            onModel = { picked ->
                model = picked
                // The new model may not offer the picked effort.
                if (effortsFor(catalog, picked).none { it.id == effort }) effort = ""
            },
            onEffort = { effort = it },
            onToggleFavorite = { scope.launch { presetStore.toggle(current) } },
            onDismiss = { showPicker = false },
        )
    }

    Scaffold(
        topBar = {
            TopAppBar(
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
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
                        presets = saved,
                        current = current,
                        onSelect = {
                            provider = it.provider
                            model = it.model
                            effort = it.effort
                        },
                        onCustomize = { showPicker = true },
                    )
                    OutlinedTextField(
                        value = prompt,
                        onValueChange = { prompt = it },
                        enabled = !starting && !checking,
                        label = { Text("First prompt (optional)") },
                        maxLines = 4,
                        modifier = Modifier.fillMaxWidth(),
                    )
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Checkbox(checked = trust, onCheckedChange = { trust = it })
                        Text(
                            "Trust this folder (answer the agent's trust prompt)",
                            style = MaterialTheme.typography.bodySmall,
                            modifier = Modifier.weight(1f).clickable { trust = !trust },
                        )
                    }
                    Button(
                        enabled = path.isNotEmpty() && !starting && !checking && !loading && pendingStart == null,
                        modifier = Modifier.fillMaxWidth(),
                        onClick = {
                            // Capture all selections before suspension; confirmation must
                            // start exactly the request that was checked.
                            val request = StartSessionRequest(
                                provider, path, prompt.trim(), trust,
                                model.ifEmpty { null }, effort.ifEmpty { null },
                            )
                            val preset = current
                            scope.launch {
                                checking = true
                                error = null
                                try {
                                    if (needsDirectoryConfirmation(request) { api.checkDirectory(it.cwd, it.prompt) }) {
                                        pendingStart = request to preset
                                    } else {
                                        start(request, preset)
                                    }
                                } finally {
                                    checking = false
                                }
                            }
                        },
                    ) {
                        if (starting || checking) {
                            CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                            Text(if (starting) "  Starting…" else "  Checking folder…")
                        } else {
                            Text(
                                "Start ${providerName(provider)} in ${path.substringAfterLast('/').ifEmpty { "…" }}",
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                            )
                        }
                    }
                }
            }
        },
    ) { padding ->
        Column(Modifier.padding(padding).fillMaxSize()) {
            DirectoryShortcutsRow(
                shortcuts = shortcuts,
                currentPath = path,
                enabled = !loading && !checking && !starting,
                onOpen = { scope.launch { load(it) } },
                modifier = Modifier.padding(top = 8.dp),
            )
            Row(
                verticalAlignment = Alignment.CenterVertically,
                modifier = Modifier.padding(start = 8.dp, end = 8.dp),
            ) {
                IconButton(
                    enabled = listing?.parent != null && !loading && !checking && !starting,
                    onClick = { scope.launch { load(listing?.parent.orEmpty()) } },
                ) { Icon(Icons.Default.ArrowUpward, contentDescription = "Parent folder") }
                Text(
                    path.ifEmpty { "Workspace roots" },
                    fontFamily = FontFamily.Monospace,
                    style = MaterialTheme.typography.bodySmall,
                    maxLines = 2,
                    overflow = TextOverflow.StartEllipsis,
                    modifier = Modifier.weight(1f),
                )
                val favoriteDir = path in shortcuts.favorites
                IconButton(
                    enabled = path.isNotEmpty(),
                    onClick = { scope.launch { shortcutStore.toggleFavorite(path) } },
                ) {
                    Icon(
                        if (favoriteDir) Icons.Default.Star else Icons.Default.StarBorder,
                        contentDescription = if (favoriteDir) "Remove from favorites" else "Add to favorites",
                    )
                }
                IconButton(enabled = path.isNotEmpty() && !loading && !checking && !starting, onClick = { showMkdir = true }) {
                    Icon(Icons.Default.CreateNewFolder, contentDescription = "New folder")
                }
            }
            error?.let {
                Text(it, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(horizontal = 16.dp))
            }
            HorizontalDivider()
            if (loading && listing == null) {
                CircularProgressIndicator(Modifier.padding(24.dp))
            }
            LazyColumn(Modifier.fillMaxSize()) {
                val entries = listing?.entries.orEmpty()
                if (entries.isEmpty() && !loading) {
                    item { Text("No subfolders", modifier = Modifier.padding(16.dp)) }
                }
                items(entries, key = { it.path }) { dir ->
                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        modifier = Modifier
                            .fillMaxWidth()
                            .clickable(enabled = !loading && !checking && !starting) { scope.launch { load(dir.path) } }
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
