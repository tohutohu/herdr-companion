package com.tohutohu.herdrcompanion.ui.newsession

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
import com.tohutohu.herdrcompanion.container
import com.tohutohu.herdrcompanion.data.AgentPreset
import com.tohutohu.herdrcompanion.data.DirectoryShortcuts
import com.tohutohu.herdrcompanion.data.lastUsedDirectory
import com.tohutohu.herdrcompanion.data.api.DirListingDto
import com.tohutohu.herdrcompanion.data.api.ModelsResponse
import com.tohutohu.herdrcompanion.data.api.StartSessionRequest
import com.tohutohu.herdrcompanion.ui.usage.UsageCardRoute
import kotlinx.coroutines.CancellationException
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
    var mode by rememberSaveable { mutableStateOf("") }
    var catalog by remember { mutableStateOf(ModelsResponse()) }
    var modelsLoading by remember { mutableStateOf(false) }
    var modelsError by remember { mutableStateOf<String?>(null) }
    var restored by rememberSaveable { mutableStateOf(false) }
    // Listing is not saveable; after rotation the restored path is loaded again.
    var directoryRestored by remember { mutableStateOf(false) }

    val loadedShortcuts = shortcuts ?: DirectoryShortcuts()
    val saved = presets?.presets.orEmpty()
    val current = withKnownNames(
        agentPreset(provider, model, effort, mode, catalog),
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
            mode = it.mode
        }
    }

    LaunchedEffect(provider) {
        // Keep the previous catalog while the replacement is loading. The
        // picker is disabled during this short interval, so the dialog keeps
        // its current height and only resizes once the new catalog is ready.
        modelsLoading = true
        modelsError = null
        try {
            val loaded = api.models(provider)
            catalog = loaded
            if (model.isNotEmpty() && loaded.models.none { it.id == model }) model = ""
            if (effort.isNotEmpty() && effortsFor(loaded, model).none { it.id == effort }) effort = ""
            // Modes belong to one agent; the other's are unknown to it.
            if (mode.isNotEmpty() && loaded.modes.none { it.id == mode }) mode = ""
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            modelsError = e.message
        }
        modelsLoading = false
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
        mode = mode,
        catalog = catalog,
        modelsLoading = modelsLoading,
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
                is NewSessionAction.SetProvider -> {
                    if (provider != action.provider) {
                        provider = action.provider
                        model = ""
                        effort = ""
                        mode = ""
                    }
                }
                is NewSessionAction.SetModel -> {
                    model = action.model
                    if (effortsFor(catalog, action.model).none { it.id == effort }) effort = ""
                }
                is NewSessionAction.SetEffort -> effort = action.effort
                is NewSessionAction.SetMode -> mode = normalizedMode(catalog, action.mode)
                is NewSessionAction.SetPrompt -> prompt = action.prompt
                is NewSessionAction.SelectPreset -> {
                    provider = action.preset.provider
                    model = action.preset.model
                    effort = action.preset.effort
                    mode = action.preset.mode
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
                            mode.ifEmpty { null },
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
        topContent = { UsageCardRoute() },
    )
}
