package com.tohutohu.herdrcompanion.data

import com.tohutohu.herdrcompanion.data.api.GatewayApi
import com.tohutohu.herdrcompanion.data.api.ProjectDto
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update

/**
 * The gateway's projects (favorite folders with their notification levels),
 * as last read, kept while the app lives so the Projects tab renders at once.
 */
class ProjectsStore(private val api: GatewayApi) {
    private val _projects = MutableStateFlow<List<ProjectDto>?>(null)

    /** Null until read once. */
    val projects: StateFlow<List<ProjectDto>?> = _projects.asStateFlow()

    private val _unsupported = MutableStateFlow(false)

    /** The gateway has no projects (older than the app). */
    val unsupported: StateFlow<Boolean> = _unsupported.asStateFlow()

    suspend fun refresh() {
        val list = api.projects()
        _unsupported.value = list == null
        _projects.value = list.orEmpty()
    }

    /** Sets a project's notification level; shown at once, put back if the gateway refuses. */
    suspend fun setNotify(path: String, level: String) {
        val before = _projects.value
        _projects.update { list -> list?.map { if (it.path == path) it.copy(notify = level) else it } }
        try {
            val saved = api.updateProject(path, notify = level)
            _projects.update { list -> list?.map { if (it.path == path) saved else it } }
        } catch (e: Exception) {
            _projects.value = before
            throw e
        }
    }
}
