package com.tohutohu.herdrmobile.ui.settings

import androidx.compose.animation.Crossfade
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
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.TextButton
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
import com.tohutohu.herdrmobile.data.PairingInvitation
import com.tohutohu.herdrmobile.data.PairingClient
import com.tohutohu.herdrmobile.data.api.GatewayApi
import com.google.mlkit.vision.codescanner.GmsBarcodeScanning
import com.google.mlkit.vision.codescanner.GmsBarcodeScannerOptions
import com.google.mlkit.vision.barcode.common.Barcode
import kotlinx.coroutines.tasks.await
import kotlinx.coroutines.CancellationException
import okhttp3.OkHttpClient
import android.app.Activity
import kotlin.system.exitProcess
import com.tohutohu.herdrmobile.ui.ExpandingContent
import kotlinx.coroutines.launch
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.imePadding

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(onDone: () -> Unit) {
    val context = LocalContext.current
    val container = context.container
    val current by container.settings.state.collectAsState()
    var url by rememberSaveable { mutableStateOf(current.gatewayUrl) }
    var token by rememberSaveable { mutableStateOf(current.token) }
    var testResult by remember { mutableStateOf<String?>(null) }
    val pushStatus by container.pushRegistration.status.collectAsState()
    val scope = rememberCoroutineScope()
    var invitation by remember { mutableStateOf<PairingInvitation?>(null) }
    var busy by remember { mutableStateOf(false) }
    var restart by rememberSaveable { mutableStateOf(false) }

    if (restart) {
        AlertDialog(
            onDismissRequest = {},
            title = { Text("Connection saved") },
            text = { Text("Close and reopen Herdr Mobile to apply this connection and its notification settings. The previous conversation cache will be cleared.") },
            confirmButton = { TextButton(onClick = {
                (context as? Activity)?.finishAndRemoveTask()
                exitProcess(0)
            }) { Text("Close app") } },
        )
    }
    invitation?.let { scanned ->
        AlertDialog(
            onDismissRequest = { if (!busy) invitation = null },
            title = { Text("Pair with this Mac?") },
            text = { Text("${scanned.gateway}\n\nThis app will be able to view and control sessions on this Mac. Firebase and Jev keys are optional.") },
            confirmButton = { TextButton(enabled = !busy, onClick = {
                busy = true
                scope.launch {
                    try {
                        val next = PairingClient().redeem(scanned)
                        restart = container.configure(next)
                        url = next.gatewayUrl; token = next.token; invitation = null
                        if (!restart) onDone()
                    } catch (e: CancellationException) { throw e
                    } catch (e: Exception) { testResult = e.message ?: "Pairing failed"; invitation = null
                    } finally { busy = false }
                }
            }) { Text("Pair") } },
            dismissButton = { TextButton(enabled = !busy, onClick = { invitation = null }) { Text("Cancel") } },
        )
    }

    Scaffold(topBar = { TopAppBar(title = { Text("Gateway settings") }) }) { padding ->
        Column(
            Modifier
                .padding(padding)
                // Scaffold leaves the keyboard out; pad for it so the fields
                // and Save scroll clear of it.
                .consumeWindowInsets(padding)
                .imePadding()
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Text(
                "Connect to herdr-mobile-gateway on your Mac over Tailscale.",
                style = MaterialTheme.typography.bodyMedium,
            )
            Button(enabled = !busy && !restart, onClick = {
                scope.launch {
                    busy = true
                    try {
                        val options = GmsBarcodeScannerOptions.Builder().setBarcodeFormats(Barcode.FORMAT_QR_CODE).enableAutoZoom().build()
                        val scanned = GmsBarcodeScanning.getClient(context, options).startScan().await()
                        invitation = PairingInvitation.parse(scanned.rawValue.orEmpty())
                    } catch (e: CancellationException) { throw e
                    } catch (e: Exception) { testResult = "Could not scan QR: ${e.message}"
                    } finally { busy = false }
                }
            }) { Text("Scan Mac QR") }
            Text("On the Mac: Herdr Mobile → ペアリングQRを表示. No Firebase or Jev key is required for pairing.", style = MaterialTheme.typography.bodySmall)
            OutlinedTextField(
                value = url,
                onValueChange = { url = it },
                label = { Text("Gateway URL") },
                placeholder = { Text("http://my-mac.tailnet-name.ts.net:8765") },
                singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, autoCorrectEnabled = false),
                modifier = Modifier.fillMaxWidth(),
            )
            OutlinedTextField(
                value = token,
                onValueChange = { token = it },
                label = { Text("Auth token") },
                supportingText = { Text("Run `herdr-mobile-gateway token` on the Mac") },
                singleLine = true,
                visualTransformation = PasswordVisualTransformation(),
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, autoCorrectEnabled = false),
                modifier = Modifier.fillMaxWidth(),
            )
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedButton(enabled = !busy && !restart, onClick = {
                    scope.launch {
                        testResult = "Testing…"
                        testResult = try {
                            val candidate = Settings(url, token)
                            val n = GatewayApi(OkHttpClient()) { candidate }.sessions().size
                            "Connected: $n sessions"
                        } catch (e: Exception) {
                            "Failed: ${e.message}"
                        }
                    }
                }) { Text("Test connection") }
                Button(
                    enabled = url.isNotBlank() && token.isNotBlank() && !busy && !restart,
                    onClick = {
                        scope.launch {
                            busy = true
                            try {
                                val next = if (url.trim() == current.gatewayUrl && token.trim() == current.token) current else Settings(url, token, connectionId = java.util.UUID.randomUUID().toString())
                                restart = container.configure(next)
                                if (!restart) onDone()
                            } catch (e: CancellationException) { throw e
                            } catch (e: Exception) { testResult = e.message ?: "Could not save settings"
                            } finally { busy = false }
                        }
                    },
                ) { Text("Save") }
            }
            ExpandingContent(value = testResult) { result ->
                Crossfade(result, label = "testResult") { Text(it, style = MaterialTheme.typography.bodyMedium) }
            }
            if (current.gatewayUrl.isNotBlank() && current.token.isNotBlank()) AgentUpdatesSection()
            ExpandingContent(value = pushStatus.takeIf { it.isNotEmpty() }) {
                Text(it, style = MaterialTheme.typography.bodySmall)
            }
        }
    }
}
