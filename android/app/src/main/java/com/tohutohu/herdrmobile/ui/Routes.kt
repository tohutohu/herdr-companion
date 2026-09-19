package com.tohutohu.herdrmobile.ui

import androidx.navigation3.runtime.NavKey
import kotlinx.serialization.Serializable

/**
 * Every screen of the app. Sealed so the back stack is saved with a
 * generated polymorphic serializer instead of looking classes up by name,
 * which R8 renaming would make fragile.
 */
@Serializable
sealed interface Route : NavKey

@Serializable
data object SessionsRoute : Route

@Serializable
data object SettingsRoute : Route

@Serializable
data class DetailRoute(
    val sessionId: String,
    /** Opened from a notification: start reading the newest message from its top. */
    val focusLatest: Boolean = false,
) : Route

@Serializable
data class FileRoute(val sessionId: String, val path: String, val line: Int = 0) : Route

@Serializable
data class ImageRoute(val url: String) : Route

@Serializable
data class TerminalRoute(val sessionId: String = "", val paneId: String? = null) : Route

@Serializable
data object NewSessionRoute : Route

@Serializable
data object ArchivedRoute : Route

@Serializable
data class StartingRoute(val startId: String) : Route
