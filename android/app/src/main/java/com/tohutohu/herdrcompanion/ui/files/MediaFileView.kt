package com.tohutohu.herdrcompanion.ui.files

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.ProgressiveMediaSource
import androidx.media3.ui.PlayerView
import com.tohutohu.herdrcompanion.container
import com.tohutohu.herdrcompanion.data.api.FileInfoDto

@androidx.annotation.OptIn(UnstableApi::class)
@Composable
internal fun MediaFileView(sessionId: String, info: FileInfoDto, onDownload: () -> Unit) {
    val context = LocalContext.current
    val api = context.container.api
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    var failed by remember(sessionId, info.path) { mutableStateOf(false) }
    val player = remember(sessionId, info.path) {
        val request = api.mediaRequest(sessionId, info.path)
        val source = DefaultHttpDataSource.Factory()
            .setDefaultRequestProperties(request.headers.toMap())
        ExoPlayer.Builder(context).build().apply {
            setAudioAttributes(AudioAttributes.Builder().setUsage(C.USAGE_MEDIA).build(), true)
            setHandleAudioBecomingNoisy(true)
            // API URLs have no media extension; explicitly use progressive playback.
            setMediaSource(ProgressiveMediaSource.Factory(source).createMediaSource(MediaItem.fromUri(request.url.toString())))
        }
    }
    DisposableEffect(player, lifecycle) {
        val listener = object : Player.Listener {
            override fun onPlayerError(error: PlaybackException) { failed = true }
        }
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_STOP) player.pause()
        }
        player.addListener(listener)
        lifecycle.addObserver(observer)
        player.prepare()
        onDispose {
            lifecycle.removeObserver(observer)
            player.removeListener(listener)
            player.release()
        }
    }
    Column(Modifier.fillMaxSize()) {
        if (failed) {
            Text(
                "Could not play this file. Download it to open with another app.",
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier.padding(16.dp),
            )
        } else {
            AndroidView(
                factory = { ctx ->
                    PlayerView(ctx).apply {
                        this.player = player
                        setShowBuffering(PlayerView.SHOW_BUFFERING_WHEN_PLAYING)
                        controllerShowTimeoutMs = 0
                    }
                },
                onRelease = { it.player = null },
                modifier = Modifier.fillMaxWidth().weight(1f),
            )
        }
        Button(onClick = { player.pause(); onDownload() }, modifier = Modifier.padding(16.dp)) { Text("Download / Open externally") }
    }
}
