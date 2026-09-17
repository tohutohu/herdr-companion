package com.tohutohu.herdrmobile.ui

import kotlinx.serialization.Serializable

@Serializable
object SessionsRoute

@Serializable
object SettingsRoute

@Serializable
data class DetailRoute(val sessionId: String)

@Serializable
data class FileRoute(val sessionId: String, val path: String, val line: Int = 0)

@Serializable
data class ImageRoute(val url: String)

@Serializable
data class TerminalRoute(val sessionId: String)

@Serializable
object NewSessionRoute

@Serializable
object ArchivedRoute
