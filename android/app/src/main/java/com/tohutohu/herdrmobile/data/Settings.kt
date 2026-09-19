package com.tohutohu.herdrmobile.data

import android.content.Context
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json

private val Context.dataStore by preferencesDataStore("settings")

class SettingsStore(private val context: Context, scope: CoroutineScope) {
    private val urlKey = stringPreferencesKey("gateway_url")
    private val tokenKey = stringPreferencesKey("token")
    private val firebaseKey = stringPreferencesKey("firebase")
    private val connectionKey = stringPreferencesKey("connection_id")
    private val gatewayIdKey = stringPreferencesKey("gateway_id")
    private val pendingKey = stringPreferencesKey("pending_connection")
    private val resetKey = booleanPreferencesKey("reset_cache")
    private val json = Json { ignoreUnknownKeys = true }

    private val flow = context.dataStore.data.map {
        Settings(it[urlKey].orEmpty(), it[tokenKey].orEmpty(),
            it[firebaseKey]?.let { value -> runCatching { json.decodeFromString<FirebaseSettings>(value) }.getOrNull() },
            it[connectionKey].orEmpty(), it[gatewayIdKey].orEmpty())
    }

    // Loaded synchronously once so that workers started from a push can use it,
    // and updated synchronously on save so callers see the new value at once.
    private val _state = MutableStateFlow(runBlocking {
        context.dataStore.data.first()[pendingKey]?.let { pending ->
            val next = json.decodeFromString<Settings>(pending)
            context.dataStore.edit {
                it[urlKey] = next.gatewayUrl.trim(); it[tokenKey] = next.token.trim()
                it[connectionKey] = next.connectionId; it[gatewayIdKey] = next.gatewayId
                if (next.firebase == null) it.remove(firebaseKey) else it[firebaseKey] = json.encodeToString(next.firebase)
                it.remove(pendingKey); it[resetKey] = true
            }
        }
        flow.first()
    })
    val state: StateFlow<Settings> = _state.asStateFlow()

    val current: Settings get() = state.value

    suspend fun save(settings: Settings) {
        write(settings)
        _state.value = settings.copy(gatewayUrl = settings.gatewayUrl.trim(), token = settings.token.trim())
    }

    /** Apply changes to an existing connection on a fresh process, before workers or Firebase start. */
    suspend fun stage(settings: Settings) {
        context.dataStore.edit { it[pendingKey] = json.encodeToString(settings) }
    }

    suspend fun needsCacheReset(): Boolean = context.dataStore.data.first()[resetKey] == true
    suspend fun cacheResetComplete() { context.dataStore.edit { it.remove(resetKey) } }

    private suspend fun write(settings: Settings) {
        context.dataStore.edit {
            it[urlKey] = settings.gatewayUrl.trim()
            it[tokenKey] = settings.token.trim()
            it[connectionKey] = settings.connectionId
            it[gatewayIdKey] = settings.gatewayId
            if (settings.firebase == null) it.remove(firebaseKey) else it[firebaseKey] = json.encodeToString(settings.firebase)
        }
    }
}
