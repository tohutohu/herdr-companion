package com.tohutohu.herdrmobile.ui.detail

import android.app.Application
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
private const val MAX_UPLOAD_BYTES = 20 * 1024 * 1024

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

    /**
     * Called in a loop while the screen is started (foreground only). The
     * session's notification is cleared on entering and on leaving, but not
     * the moment one arrives while the user is reading, so it can still be
     * noticed.
     */
    suspend fun pollWhileVisible() {
        Notifications.cancel(getApplication(), sessionId)
        try {
            while (true) {
                refresh()
                delay(POLL_MS)
            }
        } finally {
            Notifications.cancel(getApplication(), sessionId)
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

    fun send(text: String, attachments: List<Attachment>, onSent: () -> Unit) {
        viewModelScope.launch {
            _sending.value = true
            try {
                val ids = attachments.map { upload(it) }
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

    private suspend fun upload(attachment: Attachment): String {
        val resolver = getApplication<Application>().contentResolver
        val bytes = withContext(Dispatchers.IO) {
            resolver.openInputStream(attachment.uri)?.use { it.readBytes() }
                ?: error("cannot read ${attachment.name}")
        }
        // The gateway rejects anything larger, with a much vaguer message.
        if (bytes.size > MAX_UPLOAD_BYTES) {
            error("${attachment.name} is larger than ${MAX_UPLOAD_BYTES / (1024 * 1024)} MB")
        }
        return api.upload(bytes, attachment.mime, attachment.name)
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
