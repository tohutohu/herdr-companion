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
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExposedDropdownMenuAnchorType
import androidx.compose.material3.ExposedDropdownMenuBox
import androidx.compose.material3.ExposedDropdownMenuDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
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
import com.tohutohu.herdrmobile.data.DirectoryShortcuts
import com.tohutohu.herdrmobile.data.api.CatalogOption
import com.tohutohu.herdrmobile.data.api.DirListingDto
import com.tohutohu.herdrmobile.data.api.EffortOptionDto
import com.tohutohu.herdrmobile.data.api.ModelsResponse
import com.tohutohu.herdrmobile.data.api.StartSessionRequest
import kotlinx.coroutines.launch

private val PROVIDERS = listOf("claude" to "Claude Code", "codex" to "Codex")

/**
 * Pick a provider and a working directory (browse or create one under the
 * gateway's workspace roots), then start the agent in a new Herdr workspace.
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
    val shortcuts by shortcutStore.shortcuts.collectAsState(initial = DirectoryShortcuts())
    val scope = rememberCoroutineScope()

    var provider by rememberSaveable { mutableStateOf("claude") }
    var path by rememberSaveable { mutableStateOf("") } // "" = list of roots
    var listing by remember { mutableStateOf<DirListingDto?>(null) }
    var loading by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var prompt by rememberSaveable { mutableStateOf("") }
    var trust by rememberSaveable { mutableStateOf(true) }
    var starting by remember { mutableStateOf(false) }
    var showMkdir by remember { mutableStateOf(false) }
    // "" = the agent's default model / effort.
    var model by rememberSaveable { mutableStateOf("") }
    var effort by rememberSaveable { mutableStateOf("") }
    var catalog by remember { mutableStateOf(ModelsResponse()) }
    var modelsError by remember { mutableStateOf<String?>(null) }
    val efforts = effortsFor(catalog, model)

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
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        OptionPicker(
                            label = "Model",
                            options = catalog.models,
                            selected = model,
                            error = modelsError,
                            onSelect = { picked ->
                                model = picked
                                // The new model may not offer the picked effort.
                                if (effortsFor(catalog, picked).none { it.id == effort }) effort = ""
                            },
                            // Model names are the longer ones.
                            modifier = Modifier.weight(1.2f),
                        )
                        if (efforts.isNotEmpty()) {
                            OptionPicker(
                                label = "Effort",
                                options = efforts,
                                selected = effort,
                                error = null,
                                onSelect = { effort = it },
                                modifier = Modifier.weight(1f),
                            )
                        }
                    }
                    OutlinedTextField(
                        value = prompt,
                        onValueChange = { prompt = it },
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
                        enabled = path.isNotEmpty() && !starting,
                        modifier = Modifier.fillMaxWidth(),
                        onClick = {
                            scope.launch {
                                starting = true
                                try {
                                    val res = api.startSession(
                                        StartSessionRequest(
                                            provider, path, prompt.trim(), trust,
                                            model.ifEmpty { null }, effort.ifEmpty { null },
                                        ),
                                    )
                                    runCatching { shortcutStore.recordUsed(path) }
                                    runCatching { repo.refreshSessions() }
                                    onStarted(res.sessionId, res.warning)
                                } catch (e: Exception) {
                                    error = "Start failed: ${e.message}"
                                } finally {
                                    starting = false
                                }
                            }
                        },
                    ) {
                        if (starting) {
                            CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                            Text("  Starting…")
                        } else {
                            val name = PROVIDERS.first { it.first == provider }.second
                            Text(
                                "Start $name in ${path.substringAfterLast('/').ifEmpty { "…" }}",
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
            SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp)) {
                PROVIDERS.forEachIndexed { i, (id, label) ->
                    SegmentedButton(
                        selected = provider == id,
                        onClick = { provider = id },
                        shape = SegmentedButtonDefaults.itemShape(i, PROVIDERS.size),
                    ) { Text(label) }
                }
            }
            DirectoryShortcutsRow(
                shortcuts = shortcuts,
                currentPath = path,
                enabled = !loading,
                onOpen = { scope.launch { load(it) } },
            )
            Row(
                verticalAlignment = Alignment.CenterVertically,
                modifier = Modifier.padding(start = 8.dp, end = 8.dp),
            ) {
                IconButton(
                    enabled = listing?.parent != null && !loading,
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
                val favorite = path in shortcuts.favorites
                IconButton(
                    enabled = path.isNotEmpty(),
                    onClick = { scope.launch { shortcutStore.toggleFavorite(path) } },
                ) {
                    Icon(
                        if (favorite) Icons.Default.Star else Icons.Default.StarBorder,
                        contentDescription = if (favorite) "Remove from favorites" else "Add to favorites",
                    )
                }
                IconButton(enabled = path.isNotEmpty() && !loading, onClick = { showMkdir = true }) {
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
                            .clickable(enabled = !loading) { scope.launch { load(dir.path) } }
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

/**
 * Efforts offered for the picked model; the catalog's own list covers the
 * agent's default model and models that list none.
 */
fun effortsFor(catalog: ModelsResponse, modelId: String): List<EffortOptionDto> {
    val own = catalog.models.firstOrNull { it.id == modelId }?.efforts.orEmpty()
    return own.ifEmpty { catalog.efforts }
}

/** Model or effort dropdown; the first entry keeps the agent's own default. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun OptionPicker(
    label: String,
    options: List<CatalogOption>,
    selected: String,
    error: String?,
    onSelect: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    var expanded by remember { mutableStateOf(false) }
    val defaultName = options.firstOrNull { it.default }?.name
    val defaultLabel = if (defaultName != null) "Default ($defaultName)" else "Default"
    val current = options.firstOrNull { it.id == selected }?.name ?: defaultLabel
    ExposedDropdownMenuBox(expanded = expanded, onExpandedChange = { expanded = it }, modifier = modifier) {
        OutlinedTextField(
            value = current,
            onValueChange = {},
            readOnly = true,
            singleLine = true,
            // Two pickers share the row, so the value gets the smaller size.
            textStyle = MaterialTheme.typography.bodyMedium,
            label = { Text(label) },
            supportingText = error?.let { { Text("Could not load models: $it") } },
            isError = error != null,
            trailingIcon = { ExposedDropdownMenuDefaults.TrailingIcon(expanded = expanded) },
            modifier = Modifier
                .fillMaxWidth()
                .menuAnchor(ExposedDropdownMenuAnchorType.PrimaryNotEditable),
        )
        ExposedDropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
            DropdownMenuItem(
                text = { Text(defaultLabel) },
                onClick = {
                    onSelect("")
                    expanded = false
                },
            )
            options.forEach { m ->
                DropdownMenuItem(
                    text = {
                        Column {
                            Text(m.name)
                            m.description?.takeIf { it.isNotBlank() }?.let {
                                Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            }
                        }
                    },
                    onClick = {
                        onSelect(m.id)
                        expanded = false
                    },
                )
            }
        }
    }
}
