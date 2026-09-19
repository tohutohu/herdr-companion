package com.tohutohu.herdrmobile.ui.terminal

import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import com.tohutohu.herdrmobile.container
import com.tohutohu.herdrmobile.data.api.TerminalInput
import com.tohutohu.herdrmobile.ui.ExpandingContent
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

private const val TERMINAL_POLL_MS = 2_000L

/** Key buttons: label to Herdr key name. */
private val KEYS = listOf(
    "Enter" to "enter", "Esc" to "esc", "↑" to "up", "↓" to "down", "←" to "left", "→" to "right",
    "Tab" to "tab", "⇧Tab" to "shift+tab", "Space" to "space", "⌫" to "backspace", "^C" to "ctrl+c",
    "1" to "1", "2" to "2", "3" to "3", "y" to "y", "n" to "n",
)

/**
 * Recent pane output plus simple input. Not a terminal emulator: a fallback
 * for dialogs the gateway cannot answer natively.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TerminalScreen(sessionId: String, onBack: () -> Unit, paneId: String? = null) {
    val api = LocalContext.current.container.api
    var text by remember { mutableStateOf("") }
    var error by remember { mutableStateOf<String?>(null) }
    var input by remember { mutableStateOf("") }
    val scope = rememberCoroutineScope()
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val vScroll = rememberScrollState()

    suspend fun refresh() {
        try {
            text = (if (paneId != null) api.launchTerminal(paneId) else api.terminal(sessionId)).text.trimEnd()
            error = null
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            error = e.message
        }
    }

    fun send(input: TerminalInput) {
        scope.launch {
            try {
                if (paneId != null) api.launchTerminalInput(paneId, input) else api.terminalInput(sessionId, input)
                delay(300)
                refresh()
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                error = e.message
            }
        }
    }

    LaunchedEffect(lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) {
                refresh()
                delay(TERMINAL_POLL_MS)
            }
        }
    }
    LaunchedEffect(text) { vScroll.scrollTo(vScroll.maxValue) }

    Scaffold(
        topBar = {
            TopAppBar(
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
                },
                title = { Text("Terminal") },
            )
        },
        bottomBar = {
            Surface(tonalElevation = 3.dp) {
                Column(Modifier.navigationBarsPadding().imePadding().padding(8.dp)) {
                    Row(
                        Modifier.horizontalScroll(rememberScrollState()),
                        horizontalArrangement = Arrangement.spacedBy(4.dp),
                    ) {
                        KEYS.forEach { (label, key) ->
                            FilledTonalButton(onClick = { send(TerminalInput(keys = listOf(key))) }) { Text(label) }
                        }
                    }
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        OutlinedTextField(
                            value = input,
                            onValueChange = { input = it },
                            placeholder = { Text("Type text (sent with Enter)") },
                            singleLine = true,
                            modifier = Modifier.weight(1f),
                        )
                        IconButton(enabled = input.isNotEmpty(), onClick = {
                            send(TerminalInput(text = input, keys = listOf("enter")))
                            input = ""
                        }) { Icon(Icons.AutoMirrored.Filled.Send, contentDescription = "Send") }
                    }
                }
            }
        },
    ) { padding ->
        Column(Modifier.padding(padding).fillMaxSize().background(Color(0xFF111111))) {
            ExpandingContent(value = error) { Text(it, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(8.dp)) }
            SelectionContainer {
                Text(
                    text,
                    color = Color(0xFFE0E0E0),
                    fontFamily = FontFamily.Monospace,
                    fontSize = 11.sp,
                    softWrap = false,
                    modifier = Modifier
                        .fillMaxWidth()
                        .verticalScroll(vScroll)
                        .horizontalScroll(rememberScrollState())
                        .padding(8.dp),
                )
            }
        }
    }
}
