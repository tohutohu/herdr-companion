package com.tohutohu.herdrmobile.ui.settings

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrmobile.container
import com.tohutohu.herdrmobile.data.Settings
import kotlinx.coroutines.launch

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(onDone: () -> Unit) {
    val container = LocalContext.current.container
    val current by container.settings.state.collectAsState()
    var url by rememberSaveable { mutableStateOf(current.gatewayUrl) }
    var token by rememberSaveable { mutableStateOf(current.token) }
    var testResult by remember { mutableStateOf<String?>(null) }
    val pushStatus by container.pushRegistration.status.collectAsState()
    val scope = rememberCoroutineScope()

    Scaffold(topBar = { TopAppBar(title = { Text("Gateway settings") }) }) { padding ->
        Column(
            Modifier
                .padding(padding)
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Text(
                "Connect to herdr-mobile-gateway on your Mac over Tailscale.",
                style = MaterialTheme.typography.bodyMedium,
            )
            OutlinedTextField(
                value = url,
                onValueChange = { url = it },
                label = { Text("Gateway URL") },
                placeholder = { Text("http://my-mac.tailnet-name.ts.net:8765") },
                singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
                modifier = Modifier.fillMaxWidth(),
            )
            OutlinedTextField(
                value = token,
                onValueChange = { token = it },
                label = { Text("Auth token") },
                supportingText = { Text("Run `herdr-mobile-gateway token` on the Mac") },
                singleLine = true,
                visualTransformation = PasswordVisualTransformation(),
                modifier = Modifier.fillMaxWidth(),
            )
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedButton(onClick = {
                    scope.launch {
                        container.settings.save(Settings(url, token))
                        testResult = "Testing…"
                        testResult = try {
                            val n = container.api.sessions().size
                            "Connected: $n sessions"
                        } catch (e: Exception) {
                            "Failed: ${e.message}"
                        }
                    }
                }) { Text("Test connection") }
                Button(
                    enabled = url.isNotBlank() && token.isNotBlank(),
                    onClick = {
                        scope.launch {
                            container.settings.save(Settings(url, token))
                            container.pushRegistration.registerIfPossible()
                            onDone()
                        }
                    },
                ) { Text("Save") }
            }
            testResult?.let { Text(it, style = MaterialTheme.typography.bodyMedium) }
            if (pushStatus.isNotEmpty()) {
                Text(pushStatus, style = MaterialTheme.typography.bodySmall)
            }
        }
    }
}
