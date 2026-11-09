package com.tohutohu.herdrcompanion.data

import android.content.Context
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import com.tohutohu.herdrcompanion.ui.sessions.SessionColor
import kotlinx.coroutines.flow.map

private val Context.sessionColorStore by preferencesDataStore("session_colors")

/** Independent of the session cache, so polling and archiving preserve labels. */
class SessionColorsStore(private val context: Context) {
    val colors = context.sessionColorStore.data.map { preferences ->
        preferences.asMap().mapNotNull { (key, value) ->
            SessionColor.fromKey(value as? String)?.let { key.name to it }
        }.toMap()
    }

    suspend fun set(ids: Collection<String>, color: SessionColor?) {
        context.sessionColorStore.edit { preferences ->
            ids.forEach { id ->
                val key = stringPreferencesKey(id)
                if (color == null) preferences.remove(key) else preferences[key] = color.key
            }
        }
    }
}
