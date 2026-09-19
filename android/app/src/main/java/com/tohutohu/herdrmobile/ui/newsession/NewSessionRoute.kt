package com.tohutohu.herdrmobile.ui.newsession

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import com.tohutohu.herdrmobile.container
import com.tohutohu.herdrmobile.data.AgentPreset
import com.tohutohu.herdrmobile.data.DirectoryShortcuts
import com.tohutohu.herdrmobile.data.lastUsedDirectory
import com.tohutohu.herdrmobile.data.api.DirListingDto
import com.tohutohu.herdrmobile.data.api.ModelsResponse
import com.tohutohu.herdrmobile.data.api.StartSessionRequest
import com.tohutohu.herdrmobile.ui.usage.UsageCard
import kotlinx.coroutines.launch

/** Android route: owns stores, gateway calls, loading and start side effects. */
@Composable
fun NewSessionRoute(
    onBack: () -> Unit,
    onStarted: (startId: String) -> Unit,
) {
    val context = LocalContext.current
    val container = context.container
    val api = container.api
    val starts = container.sessionStarts
    val shortcutStore = container.directoryShortcuts
    val presetStore = container.agentPresets
    val shortcuts by shortcutStore.shortcuts.collectAsState(initial = null)
    val presets by presetStore.presets.collectAsState(initial = null)
    val scope = rememberCoroutineScope()

    var provider by rememberSaveable { mutableStateOf("claude") }
    var path by rememberSaveable { mutableStateOf("") }
    var listing by remember { mutableStateOf<DirListingDto?>(null) }
    var loading by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var prompt by rememberSaveable { mutableStateOf("") }
    var starting by remember { mutableStateOf(false) }
    var checking by remember { mutableStateOf(false) }
    var pendingStart by remember { mutableStateOf<PendingNewSessionStart?>(null) }
    var showMkdir by remember { mutableStateOf(false) }
    var showPicker by remember { mutableStateOf(false) }
    var model by rememberSaveable { mutableStateOf("") }
    var effort by rememberSaveable { mutableStateOf("") }
    var catalog by remember { mutableStateOf(ModelsResponse()) }
    var modelsError by remember { mutableStateOf<String?>(null) }
    var restored by rememberSaveable { mutableStateOf(false) }
    // Listing is not saveable; after rotation the restored path is loaded again.
    var directoryRestored by remember { mutableStateOf(false) }

    val loadedShortcuts = shortcuts ?: DirectoryShortcuts()
    val saved = presets?.presets.orEmpty()
    val current = withKnownNames(
        agentPreset(provider, model, effort, catalog),
        saved + listOfNotNull(presets?.lastUsed),
    )
    val favorite = saved.any { it.key == current.key }

    fun start(request: StartSessionRequest, preset: AgentPreset) {
        if (starting) return
        starting = true
        val id = starts.enqueue(request)
        container.scope.launch {
            runCatching { shortcutStore.recordUsed(request.cwd) }
            runCatching { presetStore.recordUsed(preset) }
        }
        onStarted(id)
    }

    suspend fun load(p: String) {
        loading = true
        try {
            val l = api.directories(p.ifEmpty { null })
            listing = l
            path = l.path
            error = null
            if (l.path.isEmpty() && l.entries.size == 1) {
                load(l.entries.first().path)
            }
        } catch (e: Exception) {
            error = e.message
        } finally {
            loading = false
        }
    }

    LaunchedEffect(shortcuts) {
        val loaded = shortcuts ?: return@LaunchedEffect
        if (directoryRestored) return@LaunchedEffect
        directoryRestored = true
        load(path.ifEmpty { lastUsedDirectory(loaded) })
    }

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

    val state = NewSessionUiState(
        provider = provider,
        path = path,
        listing = listing,
        loading = loading,
        error = error,
        prompt = prompt,
        starting = starting,
        checking = checking,
        pendingStart = pendingStart,
        showMkdir = showMkdir,
        showPicker = showPicker,
        model = model,
        effort = effort,
        catalog = catalog,
        modelsError = modelsError,
        shortcuts = loadedShortcuts,
        savedPresets = saved,
        currentPreset = current,
        favorite = favorite,
    )

    NewSessionScreen(
        state = state,
        onAction = { action ->
            when (action) {
                NewSessionAction.Back -> onBack()
                is NewSessionAction.SetProvider -> provider = action.provider
                is NewSessionAction.SetModel -> {
                    model = action.model
                    if (effortsFor(catalog, action.model).none { it.id == effort }) effort = ""
                }
                is NewSessionAction.SetEffort -> effort = action.effort
                is NewSessionAction.SetPrompt -> prompt = action.prompt
                is NewSessionAction.SelectPreset -> {
                    provider = action.preset.provider
                    model = action.preset.model
                    effort = action.preset.effort
                }
                is NewSessionAction.ReorderPresets -> scope.launch { presetStore.setOrder(action.presets) }
                is NewSessionAction.ReorderFavoriteDirectories -> scope.launch { shortcutStore.setFavoriteOrder(action.paths) }
                NewSessionAction.CustomizeAgent -> showPicker = true
                NewSessionAction.ToggleFavorite -> scope.launch { presetStore.toggle(current) }
                NewSessionAction.ToggleFavoriteDirectory -> scope.launch { shortcutStore.toggleFavorite(path) }
                is NewSessionAction.OpenDirectory -> scope.launch { load(action.path) }
                NewSessionAction.OpenParent -> scope.launch { load(listing?.parent.orEmpty()) }
                NewSessionAction.ShowMkdir -> showMkdir = true
                NewSessionAction.CancelMkdir -> showMkdir = false
                is NewSessionAction.CreateDirectory -> {
                    showMkdir = false
                    scope.launch {
                        try {
                            load(api.createDirectory(path, action.name.trim()))
                        } catch (e: Exception) {
                            error = "Could not create folder: ${e.message}"
                        }
                    }
                }
                NewSessionAction.DismissPicker -> showPicker = false
                NewSessionAction.StartAnyway -> {
                    pendingStart?.let { pending ->
                        pendingStart = null
                        scope.launch { start(pending.request, pending.preset) }
                    }
                }
                NewSessionAction.ChangeFolder -> pendingStart = null
                NewSessionAction.Start -> {
                    if (path.isNotEmpty() && !starting && !checking && !loading && pendingStart == null) {
                        val request = StartSessionRequest(
                            provider,
                            path,
                            prompt.trim(),
                            trust = false,
                            model.ifEmpty { null },
                            effort.ifEmpty { null },
                        )
                        val preset = current
                        scope.launch {
                            checking = true
                            error = null
                            try {
                                if (needsDirectoryConfirmation(request) { api.checkDirectory(it.cwd, it.prompt) }) {
                                    pendingStart = PendingNewSessionStart(request, preset)
                                } else {
                                    start(request, preset)
                                }
                            } finally {
                                checking = false
                            }
                        }
                    }
                }
            }
        },
        topContent = { UsageCard() },
    )
}
