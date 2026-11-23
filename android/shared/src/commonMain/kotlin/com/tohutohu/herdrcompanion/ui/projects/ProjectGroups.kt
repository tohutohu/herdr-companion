package com.tohutohu.herdrcompanion.ui.projects

import com.tohutohu.herdrcompanion.data.api.NotifyLevel
import com.tohutohu.herdrcompanion.data.api.ProjectDto
import com.tohutohu.herdrcompanion.data.api.ResidentTaskDto
import com.tohutohu.herdrcompanion.data.api.Status
import com.tohutohu.herdrcompanion.data.api.TaskState
import com.tohutohu.herdrcompanion.ui.sessions.SessionListItemUiState

/** The key of the group for what belongs to no project. */
const val OTHER_PROJECT_KEY = ""

/**
 * A project with what belongs to it. [project] is null for "その他": the
 * sessions and tasks in no registered folder.
 */
data class ProjectGroup(
    val project: ProjectDto?,
    val sessions: List<SessionListItemUiState>,
    /** Null while the resident tasks are not known (not loaded, or no support). */
    val tasks: List<ResidentTaskDto>?,
) {
    val key: String get() = project?.path ?: OTHER_PROJECT_KEY
    val name: String get() = project?.let { p -> p.name.ifBlank { p.path.substringAfterLast('/') } } ?: "その他"
    val notify: String get() = project?.notify ?: NotifyLevel.ALL

    /** Sessions and tasks at work. */
    val running: Int
        get() = sessions.count { it.session.status == Status.RUNNING } +
            tasks.orEmpty().count { it.state == TaskState.RUNNING || it.state == TaskState.QUEUED }

    /** Sessions and tasks waiting for the user: a question, an approval, a failure. */
    val needsYou: Int
        get() = sessions.count { it.session.status in NEEDS_YOU } +
            tasks.orEmpty().count { it.state == TaskState.WAITING_USER || it.state == TaskState.NEEDS_ATTENTION }

    /** Sessions with an unread turn and tasks with unread messages. */
    val unread: Int
        get() = sessions.count { it.session.unread } + tasks.orEmpty().count { it.unread > 0 }
}

private val NEEDS_YOU = setOf(Status.WAITING_INPUT, Status.WAITING_APPROVAL, Status.FAILED)

/**
 * Groups [sessions] and [tasks] by project, in the order of [projects], with
 * "その他" last when anything belongs to no project (or to one no longer
 * registered). Every registered project is listed, even an empty one.
 */
fun groupByProject(
    projects: List<ProjectDto>,
    sessions: List<SessionListItemUiState>,
    tasks: List<ResidentTaskDto>?,
): List<ProjectGroup> {
    val known = projects.mapTo(HashSet()) { it.path }
    fun keyOf(path: String?) = path?.takeIf { it in known } ?: OTHER_PROJECT_KEY
    val sessionsBy = sessions.groupBy { keyOf(it.session.projectPath) }
    val tasksBy = tasks?.groupBy { keyOf(it.projectPath) }
    val groups = projects.map { p ->
        ProjectGroup(p, sessionsBy[p.path].orEmpty(), tasksBy?.let { it[p.path].orEmpty() })
    }
    val otherSessions = sessionsBy[OTHER_PROJECT_KEY].orEmpty()
    val otherTasks = tasksBy?.get(OTHER_PROJECT_KEY).orEmpty()
    return if (otherSessions.isEmpty() && otherTasks.isEmpty()) groups
    else groups + ProjectGroup(null, otherSessions, tasksBy?.let { otherTasks })
}

/**
 * Whether a session's move to [status] is announced under its project's
 * notification [level] (null: all), as the gateway decides for pushes.
 */
fun projectWantsNotification(level: String?, status: String): Boolean = when (level) {
    NotifyLevel.OFF -> false
    NotifyLevel.ATTENTION -> status != Status.COMPLETED
    else -> true
}

/** What a notification level means, for the settings and the list. */
fun notifyLevelLabel(level: String): String = when (level) {
    NotifyLevel.ATTENTION -> "要対応のみ"
    NotifyLevel.OFF -> "オフ"
    else -> "すべて"
}
