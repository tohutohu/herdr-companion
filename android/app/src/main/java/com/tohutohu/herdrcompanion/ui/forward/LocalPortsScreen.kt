package com.tohutohu.herdrcompanion.ui.forward

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.container
import com.tohutohu.herdrcompanion.data.api.LocalPortDto

/**
 * What listens on the Mac's localhost, servers started in [sessionCwd] first, plus a port typed by
 * hand. [onOpen] gets the localhost URL to show.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun LocalPortsScreen(sessionCwd: String?, onOpen: (String) -> Unit, onBack: () -> Unit) {
    val api = LocalContext.current.container.api
    var ports by remember { mutableStateOf<List<LocalPortDto>?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var reloads by remember { mutableIntStateOf(0) }
    var typed by rememberSaveable { mutableStateOf("") }
    LaunchedEffect(reloads) {
        error = null
        try {
            ports = api.localPorts().ports
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
        } catch (e: Exception) {
            error = e.message ?: e.toString()
        }
    }
    val typedPort = typed.toIntOrNull()?.takeIf { it in 1..65535 }
    fun openTyped() {
        typedPort?.let { onOpen("http://localhost:$it/") }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
                },
                title = { Text("Localhost on the Mac") },
                actions = {
                    IconButton(onClick = { reloads++ }) { Icon(Icons.Default.Refresh, contentDescription = "Refresh") }
                },
            )
        },
    ) { padding ->
        LazyColumn(
            modifier = Modifier.consumeWindowInsets(padding).fillMaxSize().imePadding(),
            contentPadding = padding,
        ) {
            item {
                Row(
                    Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    OutlinedTextField(
                        value = typed,
                        onValueChange = { v -> typed = v.filter { it.isDigit() }.take(5) },
                        label = { Text("Port") },
                        prefix = { Text("localhost:") },
                        singleLine = true,
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number, imeAction = ImeAction.Go),
                        keyboardActions = KeyboardActions(onGo = { openTyped() }),
                        modifier = Modifier.weight(1f),
                    )
                    Button(onClick = ::openTyped, enabled = typedPort != null) { Text("Open") }
                }
            }
            val loaded = ports
            when {
                error != null -> item {
                    Text(error.orEmpty(), color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(16.dp))
                }
                loaded == null -> item {
                    Box(Modifier.fillMaxWidth().padding(24.dp), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
                }
                else -> {
                    val (inSession, others) = sessionPortsFirst(loaded, sessionCwd)
                    if (inSession.isNotEmpty()) {
                        item { SectionLabel("Started in this session's folder") }
                        items(inSession, key = { "s${it.port}" }) { PortRow(it, sessionCwd) { onOpen("http://localhost:${it.port}/") } }
                    }
                    if (others.isNotEmpty()) {
                        item { SectionLabel(if (inSession.isEmpty()) "Listening" else "Other") }
                        items(others, key = { "o${it.port}" }) { PortRow(it, sessionCwd) { onOpen("http://localhost:${it.port}/") } }
                    }
                    if (loaded.isEmpty()) {
                        item {
                            Text(
                                "Nothing listens on localhost.",
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                modifier = Modifier.padding(16.dp),
                            )
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun SectionLabel(text: String) {
    Text(
        text,
        style = MaterialTheme.typography.labelLarge,
        color = MaterialTheme.colorScheme.primary,
        modifier = Modifier.padding(start = 16.dp, end = 16.dp, top = 16.dp, bottom = 4.dp),
    )
}

@Composable
private fun PortRow(port: LocalPortDto, sessionCwd: String?, onClick: () -> Unit) {
    ListItem(
        modifier = Modifier.clickable(onClick = onClick),
        headlineContent = { Text(":${port.port}", fontFamily = FontFamily.Monospace) },
        supportingContent = {
            val where = port.cwd?.let { cwd ->
                val root = sessionCwd?.trimEnd('/')
                when {
                    root != null && cwd.startsWith("$root/") -> "./" + cwd.removePrefix("$root/")
                    root != null && cwd == root -> "."
                    else -> cwd.replace(Regex("^/Users/[^/]+"), "~")
                }
            }
            Text(
                listOfNotNull(port.command.ifBlank { null }, where).joinToString(" · "),
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        },
    )
}
