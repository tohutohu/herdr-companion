package com.tohutohu.herdrcompanion.ui

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

/** A page on the Mac's localhost, opened through the Gateway's proxy from [sessionId]'s conversation. */
@Serializable
data class LocalhostRoute(val url: String, val sessionId: String? = null) : Route

@Serializable
data object NewSessionRoute : Route

@Serializable
data object ArchivedRoute : Route

@Serializable
data class StartingRoute(val startId: String) : Route

/** The 常駐 tab's home: the resident task list. */
@Serializable
data object ResidentHomeRoute : Route

@Serializable
data class ResidentTaskRoute(
    val taskId: String,
    /** Opened from a notification: show the conversation. */
    val fromNotification: Boolean = false,
) : Route

/** Creates a task (null, in [cwd] when given) or edits one. */
@Serializable
data class ResidentFormRoute(val taskId: String? = null, val cwd: String? = null) : Route

/** The Projects tab's home: the project list. */
@Serializable
data object ProjectsRoute : Route

/** One project; [key] is its folder, or "" for what belongs to no project. */
@Serializable
data class ProjectRoute(val key: String) : Route

/** A new session in [cwd], started from a project. */
@Serializable
data class ProjectNewSessionRoute(val cwd: String) : Route
