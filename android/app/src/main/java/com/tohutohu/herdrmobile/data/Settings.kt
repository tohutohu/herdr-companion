package com.tohutohu.herdrmobile.data

import android.content.Context
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.runBlocking

data class Settings(val gatewayUrl: String = "", val token: String = "") {
    val isConfigured get() = gatewayUrl.isNotBlank() && token.isNotBlank()
}

private val Context.dataStore by preferencesDataStore("settings")

class SettingsStore(private val context: Context, scope: CoroutineScope) {
    private val urlKey = stringPreferencesKey("gateway_url")
    private val tokenKey = stringPreferencesKey("token")

    private val flow = context.dataStore.data.map {
        Settings(it[urlKey].orEmpty(), it[tokenKey].orEmpty())
    }

    // Loaded synchronously once so that workers started from a push can use it,
    // and updated synchronously on save so callers see the new value at once.
    private val _state = MutableStateFlow(runBlocking { flow.first() })
    val state: StateFlow<Settings> = _state.asStateFlow()

    val current: Settings get() = state.value

    suspend fun save(settings: Settings) {
        context.dataStore.edit {
            it[urlKey] = settings.gatewayUrl.trim()
            it[tokenKey] = settings.token.trim()
        }
        _state.value = Settings(settings.gatewayUrl.trim(), settings.token.trim())
    }
}
