package com.tohutohu.herdrcompanion.desktop

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp

/** A one-line strip under the toolbar while an update is offered or installing. */
@Composable
internal fun DesktopUpdateBanner(updater: DesktopUpdater, onShowDetails: () -> Unit) {
    val state = updater.state
    val update = when (state) {
        is DesktopUpdateState.Available -> state.update
        is DesktopUpdateState.Installing -> state.update
        is DesktopUpdateState.Failed -> state.update
        else -> null
    } ?: return
    if (state !is DesktopUpdateState.Installing && updater.bannerDismissedVersion == update.version) return
    Row(
        Modifier.fillMaxWidth().padding(horizontal = 18.dp, vertical = 2.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        when (state) {
            is DesktopUpdateState.Installing -> {
                Text("Installing Herdr Companion ${update.version}…", modifier = Modifier.weight(1f))
                UpdateProgress(state.progress, Modifier.width(160.dp))
            }
            is DesktopUpdateState.Failed -> {
                Text(state.message, color = MaterialTheme.colorScheme.error, modifier = Modifier.weight(1f))
                TextButton(onClick = onShowDetails) { Text("Details") }
                TextButton(onClick = updater::dismissBanner) { Text("Later") }
            }
            else -> {
                Text("Herdr Companion ${update.version} is available.", modifier = Modifier.weight(1f))
                TextButton(onClick = { DesktopPlatformActions.openUrl(update.releaseUrl) }) { Text("Release Notes") }
                TextButton(onClick = { updater.install(update) }) { Text("Install and Restart") }
                TextButton(onClick = updater::dismissBanner) { Text("Later") }
            }
        }
    }
}

/** Opened from "Check for Updates…"; it follows the updater through check and install. */
@Composable
internal fun DesktopUpdateDialog(updater: DesktopUpdater, onDismiss: () -> Unit) {
    val state = updater.state
    val update = when (state) {
        is DesktopUpdateState.Available -> state.update
        is DesktopUpdateState.Failed -> state.update
        else -> null
    }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Software Update") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                when (state) {
                    DesktopUpdateState.Idle, DesktopUpdateState.Checking -> {
                        Text("Checking for updates…")
                        LinearProgressIndicator(Modifier.fillMaxWidth())
                    }
                    is DesktopUpdateState.UpToDate -> Text("Herdr Companion ${state.version} is the latest version.")
                    is DesktopUpdateState.Available -> {
                        Text("Herdr Companion ${state.update.version} is available. You have ${updater.currentVersion}.")
                        Text(
                            "Herdr Companion quits, replaces itself with the new version, and opens again. " +
                                "Open sessions are restored.",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    is DesktopUpdateState.Installing -> {
                        Text(
                            if (state.progress == null) "Verifying Herdr Companion ${state.update.version}…"
                            else "Downloading Herdr Companion ${state.update.version}…",
                        )
                        UpdateProgress(state.progress, Modifier.fillMaxWidth())
                    }
                    is DesktopUpdateState.Failed -> Text(state.message, color = MaterialTheme.colorScheme.error)
                }
            }
        },
        confirmButton = {
            when {
                state is DesktopUpdateState.Available ->
                    TextButton(onClick = { updater.install(state.update) }) { Text("Install and Restart") }
                state is DesktopUpdateState.Failed && state.update != null ->
                    TextButton(onClick = { updater.install(state.update) }) { Text("Try Again") }
                state is DesktopUpdateState.Failed -> TextButton(onClick = { updater.check() }) { Text("Try Again") }
                else -> TextButton(onClick = onDismiss) { Text("OK") }
            }
        },
        dismissButton = {
            Row {
                if (update != null) {
                    TextButton(onClick = { DesktopPlatformActions.openUrl(update.releaseUrl) }) { Text("Release Notes") }
                }
                if (state is DesktopUpdateState.Available || state is DesktopUpdateState.Failed) {
                    TextButton(onClick = onDismiss) { Text("Later") }
                }
            }
        },
    )
}

@Composable
private fun UpdateProgress(progress: Float?, modifier: Modifier) {
    if (progress == null) LinearProgressIndicator(modifier) else LinearProgressIndicator(progress = { progress }, modifier = modifier)
}
