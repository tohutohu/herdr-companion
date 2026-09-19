package com.tohutohu.herdrmobile.ui.usage

import android.text.format.DateUtils
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import com.tohutohu.herdrmobile.container
import com.tohutohu.herdrmobile.data.SessionRepository
import com.tohutohu.herdrmobile.data.api.UsageDto
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/** The gateway serves a cached reading, so polling it is cheap. */
private const val USAGE_POLL_MS = 60_000L

/** The last limits reading, kept while the screen showing it is on the back stack. */
class UsageHolder : ViewModel() {
    var usage by mutableStateOf<UsageDto?>(null)
}

/**
 * How much of the Claude / Codex plan limits is used. Collapsed it is a single
 * line; tapping it shows every window with its reset time. The refresh button
 * makes the gateway read the limits again instead of serving its cache.
 */
@Composable
fun UsageCardRoute(modifier: Modifier = Modifier) {
    val api = LocalContext.current.container.api
    // Held past the composition: coming back to the list shows the last
    // reading at once instead of growing the card in again.
    val holder: UsageHolder = viewModel()
    var usage by holder::usage
    var refreshing by remember { mutableStateOf(false) }
    var expanded by rememberSaveable { mutableStateOf(false) }
    var nowMillis by remember { mutableStateOf(System.currentTimeMillis()) }
    val scope = rememberCoroutineScope()
    val lifecycle = LocalLifecycleOwner.current.lifecycle

    // A failed poll keeps the last reading on screen; the session list already
    // reports an unreachable gateway.
    LaunchedEffect(lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) {
                runCatching { api.usage() }.onSuccess { usage = it }
                delay(USAGE_POLL_MS)
            }
        }
    }

    // Reset labels should continue updating while the card is visible even
    // when the gateway returns the same cached usage snapshot.
    LaunchedEffect(lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) {
                nowMillis = System.currentTimeMillis()
                delay(USAGE_POLL_MS)
            }
        }
    }

    // Grows in with the first reading instead of pushing the list down at once.
    val shown = usage?.takeIf { it.providers.isNotEmpty() || it.error != null }
    val state = shown?.let {
        UsageCardUiState(
            current = it,
            nowMillis = nowMillis,
            expanded = expanded,
            refreshing = refreshing,
            fetchedAtText = it.fetchedAt?.let { fetchedAt ->
                "updated " + DateUtils.getRelativeTimeSpanString(SessionRepository.parseTime(fetchedAt)).toString()
            },
        )
    }
    UsageCard(
        state = state,
        modifier = modifier,
        onAction = { action ->
            when (action) {
                UsageCardAction.Toggle -> expanded = !expanded
                UsageCardAction.Refresh -> if (!refreshing) {
                    scope.launch {
                        refreshing = true
                        runCatching { api.refreshUsage() }.onSuccess { usage = it }
                        refreshing = false
                    }
                }
            }
        },
    )
}
