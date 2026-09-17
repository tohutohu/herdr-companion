package com.tohutohu.herdrmobile.ui.detail

import android.app.Application
import android.net.Uri
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.tohutohu.herdrmobile.container
import com.tohutohu.herdrmobile.data.Message
import com.tohutohu.herdrmobile.data.api.GatewayException
import com.tohutohu.herdrmobile.data.api.InteractionResponseDto
import com.tohutohu.herdrmobile.data.db.SessionEntity
import com.tohutohu.herdrmobile.push.Notifications
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private const val POLL_MS = 3_000L

class SessionDetailViewModel(app: Application, val sessionId: String) : AndroidViewModel(app) {
    private val repo = app.container.repository
    private val api = app.container.api

    val session: StateFlow<SessionEntity?> =
        repo.observeSession(sessionId).stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), null)
    val messages: StateFlow<List<Message>> =
        repo.observeMessages(sessionId).stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), emptyList())

    private val _error = MutableStateFlow<String?>(null)
    val error = _error.asStateFlow()

    private val _sending = MutableStateFlow(false)
    val sending = _sending.asStateFlow()

    private var fullSyncDone = false

    /** Called in a loop while the screen is started (foreground only). */
    suspend fun pollWhileVisible() {
        Notifications.cancel(getApplication(), sessionId)
        while (true) {
            refresh()
            delay(POLL_MS)
        }
    }

    suspend fun refresh() {
        _error.value = try {
            // The first fetch after opening replaces the cache entirely.
            repo.refreshMessages(sessionId, full = !fullSyncDone)
            fullSyncDone = true
            null
        } catch (e: GatewayException) {
            "${e.message}"
        } catch (e: Exception) {
            "Offline: showing cached messages (${e.message})"
        }
    }

    fun send(text: String, images: List<Uri>, onSent: () -> Unit) {
        viewModelScope.launch {
            _sending.value = true
            try {
                val ids = images.map { uri -> upload(uri) }
                repo.send(sessionId, text, ids)
                onSent()
                delay(500)
                refresh()
            } catch (e: Exception) {
                _error.value = "Send failed: ${e.message}"
            } finally {
                _sending.value = false
            }
        }
    }

    private suspend fun upload(uri: Uri): String {
        val resolver = getApplication<Application>().contentResolver
        val (type, bytes) = withContext(Dispatchers.IO) {
            val type = resolver.getType(uri) ?: "image/jpeg"
            val bytes = resolver.openInputStream(uri)?.use { it.readBytes() }
                ?: error("cannot read image")
            type to bytes
        }
        return api.upload(bytes, type)
    }

    fun respond(response: InteractionResponseDto) {
        viewModelScope.launch {
            _sending.value = true
            try {
                repo.respond(sessionId, response)
                delay(800)
                refresh()
            } catch (e: Exception) {
                _error.value = "Answer failed: ${e.message}"
            } finally {
                _sending.value = false
            }
        }
    }
}
