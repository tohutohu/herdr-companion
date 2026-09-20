package com.tohutohu.herdrcompanion.desktop

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.Modifier
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.ui.Alignment
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.type
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.DialogWindow
import androidx.compose.ui.window.rememberDialogState
import com.tohutohu.herdrcompanion.data.AgentPreset
import com.tohutohu.herdrcompanion.data.AgentPresets
import com.tohutohu.herdrcompanion.data.lastUsedDirectory
import com.tohutohu.herdrcompanion.data.pushRecent
import com.tohutohu.herdrcompanion.data.toggleFavorite
import com.tohutohu.herdrcompanion.data.togglePreset
import com.tohutohu.herdrcompanion.data.api.DirListingDto
import com.tohutohu.herdrcompanion.data.api.GatewayApi
import com.tohutohu.herdrcompanion.data.api.ModelsResponse
import com.tohutohu.herdrcompanion.data.api.StartSessionRequest
import com.tohutohu.herdrcompanion.data.api.StartSessionResponse
import com.tohutohu.herdrcompanion.ui.newsession.NewSessionAction
import com.tohutohu.herdrcompanion.ui.newsession.NewSessionScreen
import com.tohutohu.herdrcompanion.ui.newsession.NewSessionUiState
import com.tohutohu.herdrcompanion.ui.newsession.PendingNewSessionStart
import com.tohutohu.herdrcompanion.ui.newsession.agentPreset
import com.tohutohu.herdrcompanion.ui.newsession.effortsFor
import com.tohutohu.herdrcompanion.ui.newsession.needsDirectoryConfirmation
import com.tohutohu.herdrcompanion.ui.newsession.withKnownNames
import kotlinx.coroutines.launch

@Composable
fun DesktopNewSessionWindow(
    api: GatewayApi,
    onCreated: (String?) -> Unit,
    onDismiss: () -> Unit,
    onError: (String) -> Unit,
) {
    val scope = rememberCoroutineScope()
    val promptFocusRequester = remember { FocusRequester() }
    var shortcuts by remember { mutableStateOf(DesktopPreferences.loadDirectoryShortcuts()) }
    var presets by remember { mutableStateOf(DesktopPreferences.loadAgentPresets()) }
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
    var trustRequest by remember { mutableStateOf<StartSessionResponse?>(null) }

    suspend fun load(nextPath: String?) {
        loading = true
        try {
            val result = api.directories(nextPath?.ifBlank { null })
            listing = result
            path = result.path
            error = null
            if (result.path.isEmpty() && result.entries.size == 1) {
                load(result.entries.first().path)
            }
        } catch (cause: Exception) {
            val mapped = cause.toDesktopGatewayError()
            error = mapped.message
            onError(mapped.message)
        } finally {
            loading = false
        }
    }

    fun finish(result: StartSessionResponse) {
        if (result.trustRequired) trustRequest = result else onCreated(result.sessionId)
    }

    fun start(request: StartSessionRequest, preset: AgentPreset) {
        if (starting) return
        starting = true
        // Match Android: remember what was actually started, including the
        // last-used selection even when it is not one of the favorites.
        shortcuts = shortcuts.copy(recents = pushRecent(shortcuts.recents, request.cwd))
        DesktopPreferences.saveDirectoryShortcuts(shortcuts)
        presets = presets.copy(lastUsed = preset)
        DesktopPreferences.saveAgentPresets(presets)
        scope.launch {
            try {
                finish(api.startSession(request))
            } catch (cause: Exception) {
                val mapped = cause.toDesktopGatewayError()
                error = mapped.message
                onError(mapped.message)
            } finally {
                starting = false
            }
        }
    }

    fun answerTrust(trust: Boolean) {
        val request = trustRequest ?: return
        trustRequest = null
        starting = true
        scope.launch {
            try {
                if (trust) finish(api.answerTrust(request.paneId, true))
                else api.answerTrust(request.paneId, false)
            } catch (cause: Exception) {
                val mapped = cause.toDesktopGatewayError()
                error = mapped.message
                onError(mapped.message)
            } finally {
                starting = false
            }
        }
    }

    LaunchedEffect(Unit) {
        presets.lastUsed?.let {
            provider = it.provider
            model = it.model
            effort = it.effort
        }
        load(lastUsedDirectory(shortcuts).ifEmpty { null })
    }
    LaunchedEffect(provider) {
        catalog = ModelsResponse()
        modelsError = null
        try {
            catalog = api.models(provider)
            if (model.isNotEmpty() && catalog.models.none { it.id == model }) model = ""
            if (effort.isNotEmpty() && catalog.models.flatMap { it.efforts }.none { it.id == effort } && catalog.efforts.none { it.id == effort }) effort = ""
        } catch (cause: Exception) {
            val mapped = cause.toDesktopGatewayError()
            modelsError = mapped.message
        }
    }

    val currentPreset = withKnownNames(
        agentPreset(provider, model, effort, catalog),
        presets.presets + listOfNotNull(presets.lastUsed),
    )
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
        shortcuts = shortcuts,
        savedPresets = presets.presets,
        currentPreset = currentPreset,
        favorite = presets.presets.any { it.key == currentPreset.key },
    )

    if (trustRequest != null) {
        AlertDialog(
            onDismissRequest = { answerTrust(false) },
            title = { Text("Trust this folder?") },
            text = { Text("The agent needs permission to use this working folder. Continue starting the session?") },
            confirmButton = { TextButton(onClick = { answerTrust(true) }) { Text("Trust and continue") } },
            dismissButton = { TextButton(onClick = { answerTrust(false) }) { Text("Cancel") } },
        )
    }

    DialogWindow(
        onCloseRequest = onDismiss,
        title = "New Session",
        state = rememberDialogState(width = 900.dp, height = 720.dp),
        resizable = true,
        onKeyEvent = { event ->
            if (event.type == KeyEventType.KeyDown && event.key == Key.Escape) {
                onDismiss()
                true
            } else {
                false
            }
        },
    ) {
        LaunchedEffect(Unit) {
            promptFocusRequester.requestFocus()
        }
        Surface(Modifier.fillMaxSize()) {
            NewSessionScreen(
                state = state,
                topContent = {
                    Row(
                        Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Text("Choose a working folder from Gateway or Finder.")
                        TextButton(
                            onClick = {
                                DesktopFilePicker.pickDirectory(window)?.let { selected ->
                                    scope.launch { load(selected.toString()) }
                                }
                            },
                        ) { Text("Choose folder…") }
                    }
                },
                initialPromptFocusRequester = promptFocusRequester,
                onAction = { action ->
                    when (action) {
                        NewSessionAction.Back -> onDismiss()
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
                        is NewSessionAction.ReorderPresets -> {
                            presets = presets.copy(presets = action.presets)
                            DesktopPreferences.saveAgentPresets(presets)
                        }
                        is NewSessionAction.ReorderFavoriteDirectories -> {
                            shortcuts = shortcuts.copy(favorites = action.paths)
                            DesktopPreferences.saveDirectoryShortcuts(shortcuts)
                        }
                        NewSessionAction.ToggleFavorite -> {
                            presets = presets.copy(presets = togglePreset(presets.presets, currentPreset))
                            DesktopPreferences.saveAgentPresets(presets)
                        }
                        NewSessionAction.ToggleFavoriteDirectory -> {
                            shortcuts = shortcuts.copy(favorites = toggleFavorite(shortcuts.favorites, path))
                            DesktopPreferences.saveDirectoryShortcuts(shortcuts)
                        }
                        NewSessionAction.CustomizeAgent -> showPicker = true
                        is NewSessionAction.OpenDirectory -> scope.launch { load(action.path) }
                        NewSessionAction.OpenParent -> scope.launch { load(listing?.parent) }
                        NewSessionAction.ShowMkdir -> showMkdir = true
                        NewSessionAction.CancelMkdir -> showMkdir = false
                        is NewSessionAction.CreateDirectory -> {
                            showMkdir = false
                            scope.launch {
                                try {
                                    load(api.createDirectory(path, action.name.trim()))
                                } catch (cause: Exception) {
                                    val mapped = cause.toDesktopGatewayError()
                                    error = mapped.message
                                    onError(mapped.message)
                                }
                            }
                        }
                        NewSessionAction.DismissPicker -> showPicker = false
                        NewSessionAction.StartAnyway -> {
                            pendingStart?.let {
                                pendingStart = null
                                start(it.request, it.preset)
                            }
                        }
                        NewSessionAction.ChangeFolder -> pendingStart = null
                        NewSessionAction.Start -> {
                            if (path.isNotEmpty() && !starting && !checking && !loading && pendingStart == null) {
                                val request = StartSessionRequest(
                                    provider = provider,
                                    cwd = path,
                                    prompt = prompt.trim(),
                                    trust = false,
                                    model = model.ifEmpty { null },
                                    effort = effort.ifEmpty { null },
                                )
                                scope.launch {
                                    checking = true
                                    try {
                                        if (needsDirectoryConfirmation(request) { api.checkDirectory(it.cwd, it.prompt) }) {
                                            pendingStart = PendingNewSessionStart(request, currentPreset)
                                        } else {
                                            start(request, currentPreset)
                                        }
                                    } finally {
                                        checking = false
                                    }
                                }
                            }
                        }
                    }
                },
            )
        }
    }
}
