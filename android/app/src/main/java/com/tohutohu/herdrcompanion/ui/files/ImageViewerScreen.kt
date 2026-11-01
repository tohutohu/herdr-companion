package com.tohutohu.herdrcompanion.ui.files

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.WindowInsetsSides
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.only
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Download
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.IconButtonDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.container
import com.tohutohu.herdrcompanion.data.DownloadState

/** Fullscreen image with pinch and double-tap zoom. */
@Composable
fun ImageViewerScreen(url: String, onBack: () -> Unit) {
    val context = LocalContext.current
    val downloads = context.container.downloads
    val downloadState by remember(url) { downloads.imageState(url) }.collectAsState()
    Box(Modifier.fillMaxSize().background(Color.Black)) {
        ZoomableImage(model = url, contentDescription = "Image", modifier = Modifier.fillMaxSize())
        IconButton(
            onClick = onBack,
            colors = IconButtonDefaults.iconButtonColors(contentColor = Color.White),
            // In landscape the navigation bar or a cutout may sit on the start edge.
            modifier = Modifier.align(Alignment.TopStart)
                .windowInsetsPadding(WindowInsets.safeDrawing.only(WindowInsetsSides.Top + WindowInsetsSides.Start)),
        ) { Icon(Icons.Default.Close, contentDescription = "Close") }
        IconButton(
            onClick = { downloads.startImage(url) },
            enabled = downloadState !is DownloadState.Running,
            colors = IconButtonDefaults.iconButtonColors(contentColor = Color.White),
            modifier = Modifier.align(Alignment.TopEnd)
                .windowInsetsPadding(WindowInsets.safeDrawing.only(WindowInsetsSides.Top + WindowInsetsSides.End)),
        ) {
            when (downloadState) {
                is DownloadState.Running -> CircularProgressIndicator(
                    color = Color.White,
                    strokeWidth = 2.dp,
                    modifier = Modifier.size(22.dp),
                )
                is DownloadState.Done -> Icon(Icons.Default.Check, contentDescription = "Saved to Downloads")
                else -> Icon(Icons.Default.Download, contentDescription = "Download image")
            }
        }
        if (downloadState is DownloadState.Failed) {
            Text(
                text = (downloadState as DownloadState.Failed).message,
                color = Color.White,
                modifier = Modifier.align(Alignment.BottomCenter).padding(16.dp),
            )
        }
    }
}
