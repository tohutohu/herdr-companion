package com.tohutohu.herdrcompanion.ui.resident

import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalContext
import com.tohutohu.herdrcompanion.container
import com.tohutohu.herdrcompanion.push.Notifications

/** Android route of the 常駐 tab's task list. */
@Composable
fun ResidentHomeEntry(onOpenTask: (String) -> Unit, onCreate: () -> Unit) {
    val container = LocalContext.current.container
    val backend = remember(container.settings.current.connectionId) { container.residentBackend() }
    val state by container.residentHome.collectAsState()
    ResidentHomeScreen(
        backend = backend,
        state = state,
        onState = { container.residentHome.value = it },
        onOpenTask = onOpenTask,
        onCreate = onCreate,
    )
}

/** Android route of one task. Opening it clears the task's notification. */
@Composable
fun ResidentTaskEntry(taskId: String, fromNotification: Boolean, onBack: () -> Unit, onEdit: (String) -> Unit, onDeleted: () -> Unit) {
    val context = LocalContext.current
    val backend = remember(context.container.settings.current.connectionId) { context.container.residentBackend() }
    remember(taskId) { Notifications.cancelResident(context, taskId) }
    ResidentTaskScreen(
        backend = backend,
        taskId = taskId,
        onBack = onBack,
        onEdit = onEdit,
        onDeleted = onDeleted,
        initialTab = 0.takeIf { fromNotification } ?: 0,
    )
}

/** Android route of the create / edit form. */
@Composable
fun ResidentFormEntry(taskId: String?, onSaved: (String) -> Unit, onCancel: () -> Unit) {
    val container = LocalContext.current.container
    val backend = remember(container.settings.current.connectionId) { container.residentBackend() }
    val home by container.residentHome.collectAsState()
    val tz = (home.support as? ResidentSupport.Supported)?.defaultTimezone ?: "Asia/Tokyo"
    ResidentTaskForm(backend = backend, existingId = taskId, defaultTimezone = tz, onSaved = onSaved, onCancel = onCancel)
}
