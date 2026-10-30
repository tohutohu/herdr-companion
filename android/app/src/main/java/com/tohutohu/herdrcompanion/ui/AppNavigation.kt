package com.tohutohu.herdrcompanion.ui

import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.List
import androidx.compose.material.icons.filled.Schedule
import androidx.compose.material3.Icon
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.navigation3.runtime.rememberDecoratedNavEntries
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSerializable
import androidx.compose.ui.platform.LocalDensity
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.viewmodel.navigation3.rememberViewModelStoreNavEntryDecorator
import androidx.navigation3.runtime.NavBackStack
import androidx.navigation3.runtime.entryProvider
import androidx.navigation3.runtime.rememberSaveableStateHolderNavEntryDecorator
import androidx.navigation3.runtime.serialization.NavBackStackSerializer
import androidx.navigation3.ui.NavDisplay
import com.tohutohu.herdrcompanion.ui.detail.SessionDetailRoute
import com.tohutohu.herdrcompanion.ui.files.FileViewerScreen
import com.tohutohu.herdrcompanion.ui.files.ImageViewerRoute
import com.tohutohu.herdrcompanion.ui.resident.ResidentFormEntry
import com.tohutohu.herdrcompanion.ui.resident.ResidentHomeEntry
import com.tohutohu.herdrcompanion.ui.resident.ResidentTaskEntry
import com.tohutohu.herdrcompanion.ui.newsession.NewSessionRoute
import com.tohutohu.herdrcompanion.ui.sessions.ArchivedSessionsScreen
import com.tohutohu.herdrcompanion.ui.sessions.SessionListRoute
import com.tohutohu.herdrcompanion.ui.settings.SettingsScreen

/**
 * The app's screens, shown by Navigation 3 from back stacks that survive
 * configuration changes and process death. Sessions and 常駐 (resident
 * tasks) are top-level tabs, each with its own back stack, so switching
 * keeps where each one was. The user leaves the app through Sessions
 * ("exit through home"): back from the 常駐 home returns to it.
 *
 * [openSession] is a session to bring up (from a notification tap); it
 * replaces any session detail already on the stack, so taps do not pile
 * screens up behind the one being opened. [openResidentTask] does the same
 * for a resident task in the 常駐 tab.
 */
@Composable
fun AppNavigation(
    start: Route,
    openSession: String?,
    onSessionOpened: () -> Unit,
    openResidentTask: String? = null,
    onResidentTaskOpened: () -> Unit = {},
) {
    val backStack = rememberSerializable(serializer = NavBackStackSerializer(Route.serializer())) {
        NavBackStack(start)
    }
    val residentStack = rememberSerializable(serializer = NavBackStackSerializer(Route.serializer())) {
        NavBackStack<Route>(ResidentHomeRoute)
    }
    var residentTab by rememberSaveable { mutableStateOf(false) }
    val nav = remember(backStack) { Navigator(backStack) }
    val resident = remember(residentStack) { Navigator(residentStack) }
    LaunchedEffect(openSession) {
        openSession ?: return@LaunchedEffect
        residentTab = false
        nav.openFromNotification(DetailRoute(openSession, focusLatest = true))
        onSessionOpened()
    }
    LaunchedEffect(openResidentTask) {
        openResidentTask ?: return@LaunchedEffect
        residentTab = true
        resident.openResidentFromNotification(ResidentTaskRoute(openResidentTask, fromNotification = true))
        onResidentTaskOpened()
    }
    val density = LocalDensity.current
    val motion = remember(density) { ScreenMotion(density) }

    val provider = entryProvider<Route> {
            entry<SessionsRoute> {
                val lifecycle = LocalLifecycleOwner.current.lifecycle
                SessionListRoute(
                    onOpen = { id -> lifecycle.ifResumed { nav.push(DetailRoute(id)) } },
                    onSettings = { lifecycle.ifResumed { nav.push(SettingsRoute) } },
                    onNew = { lifecycle.ifResumed { nav.push(NewSessionRoute) } },
                    onArchived = { lifecycle.ifResumed { nav.push(ArchivedRoute) } },
                    onOpenStart = { id -> lifecycle.ifResumed { nav.push(StartingRoute(id)) } },
                )
            }
            entry<ArchivedRoute> {
                val lifecycle = LocalLifecycleOwner.current.lifecycle
                ArchivedSessionsScreen(
                    onBack = { lifecycle.ifResumed { nav.back() } },
                    onOpen = { id -> lifecycle.ifResumed { nav.push(DetailRoute(id)) } },
                )
            }
            entry<NewSessionRoute> {
                val lifecycle = LocalLifecycleOwner.current.lifecycle
                NewSessionRoute(
                    onBack = { lifecycle.ifResumed { nav.back() } },
                    onStarted = { id -> nav.replaceAbove(SessionsRoute, StartingRoute(id)) },
                )
            }
            entry<StartingRoute> { route ->
                com.tohutohu.herdrcompanion.ui.newsession.StartingSessionRoute(
                    route.startId,
                    onBack = { nav.back(orReplaceWith = SessionsRoute) },
                    onReady = { id ->
                        val at = backStack.indexOf(route)
                        if (at >= 0) backStack[at] = DetailRoute(id)
                    },
                )
            }
            entry<SettingsRoute> {
                SettingsScreen(onDone = { nav.back(orReplaceWith = SessionsRoute) })
            }
            entry<DetailRoute> { route ->
                val lifecycle = LocalLifecycleOwner.current.lifecycle
                SessionDetailRoute(
                    sessionId = route.sessionId,
                    focusLatest = route.focusLatest,
                    onOpenSession = { id -> lifecycle.ifResumed { nav.openLinkedSession(id) } },
                    onBack = { lifecycle.ifResumed { nav.back(orReplaceWith = SessionsRoute) } },
                    onOpenFile = { path, line -> lifecycle.ifResumed { nav.push(FileRoute(route.sessionId, path, line)) } },
                    onOpenImage = { url -> lifecycle.ifResumed { nav.push(ImageRoute(url)) } },
                )
            }
            entry<FileRoute> { route ->
                val lifecycle = LocalLifecycleOwner.current.lifecycle
                FileViewerScreen(route.sessionId, route.path, route.line, onBack = { lifecycle.ifResumed { nav.back() } })
            }
            entry<ImageRoute>(metadata = ScreenMotion.zoom) { route ->
                val lifecycle = LocalLifecycleOwner.current.lifecycle
                ImageViewerRoute(route.url, onBack = { lifecycle.ifResumed { nav.back() } })
            }
            entry<ResidentHomeRoute> {
                val lifecycle = LocalLifecycleOwner.current.lifecycle
                ResidentHomeEntry(
                    onOpenTask = { id -> lifecycle.ifResumed { resident.push(ResidentTaskRoute(id)) } },
                    onCreate = { lifecycle.ifResumed { resident.push(ResidentFormRoute()) } },
                )
            }
            entry<ResidentTaskRoute> { route ->
                val lifecycle = LocalLifecycleOwner.current.lifecycle
                ResidentTaskEntry(
                    taskId = route.taskId,
                    fromNotification = route.fromNotification,
                    onBack = { lifecycle.ifResumed { resident.back() } },
                    onEdit = { id -> lifecycle.ifResumed { resident.push(ResidentFormRoute(id)) } },
                    onDeleted = { resident.replaceAbove(ResidentHomeRoute, null) },
                )
            }
            entry<ResidentFormRoute> { route ->
                val lifecycle = LocalLifecycleOwner.current.lifecycle
                ResidentFormEntry(
                    taskId = route.taskId,
                    onSaved = { id ->
                        if (route.taskId == null) resident.replaceAbove(ResidentHomeRoute, ResidentTaskRoute(id)) else resident.back()
                    },
                    onCancel = { lifecycle.ifResumed { resident.back() } },
                )
            }
        }

    // Each stack keeps its screens' state (and ViewModels) while the other
    // tab is shown.
    val sessionEntries = rememberDecoratedNavEntries(
        backStack = backStack,
        entryDecorators = listOf(rememberSaveableStateHolderNavEntryDecorator(), rememberViewModelStoreNavEntryDecorator()),
        entryProvider = provider,
    )
    val residentEntries = rememberDecoratedNavEntries(
        backStack = residentStack,
        entryDecorators = listOf(rememberSaveableStateHolderNavEntryDecorator(), rememberViewModelStoreNavEntryDecorator()),
        entryProvider = provider,
    )
    val top = if (residentTab) residentStack.lastOrNull() else backStack.lastOrNull()
    val showTabs = top == SessionsRoute || top == ResidentHomeRoute
    Scaffold(
        contentWindowInsets = WindowInsets(0, 0, 0, 0),
        bottomBar = {
            if (showTabs) {
                NavigationBar {
                    NavigationBarItem(
                        selected = !residentTab,
                        onClick = { residentTab = false },
                        icon = { Icon(Icons.AutoMirrored.Filled.List, contentDescription = null) },
                        label = { Text("Sessions") },
                    )
                    NavigationBarItem(
                        selected = residentTab,
                        onClick = { residentTab = true },
                        icon = { Icon(Icons.Default.Schedule, contentDescription = null) },
                        label = { Text("常駐") },
                    )
                }
            }
        },
    ) { padding ->
        NavDisplay(
            entries = if (residentTab) sessionEntries + residentEntries else sessionEntries,
            modifier = Modifier.padding(padding).consumeWindowInsets(padding),
            onBack = {
                when {
                    !residentTab -> nav.back()
                    residentStack.size > 1 -> resident.back()
                    else -> residentTab = false
                }
            },
            transitionSpec = motion.push,
            popTransitionSpec = motion.pop,
            predictivePopTransitionSpec = motion.predictivePop,
        )
    }
}

/**
 * Runs [block] only while the screen is resumed. NavDisplay holds screens at
 * STARTED while a transition runs, so a second tap on a row or a back arrow
 * during the animation does not push the same screen twice or pop two.
 */
private inline fun Lifecycle.ifResumed(block: () -> Unit) {
    if (currentState.isAtLeast(Lifecycle.State.RESUMED)) block()
}

/** The operations the screens need on the back stack. */
internal class Navigator(private val stack: NavBackStack<Route>) {
    fun push(route: Route) {
        stack.add(route)
    }

    /** Reuse an earlier detail entry, preserving its history position and draft. */
    fun openLinkedSession(sessionId: String) {
        val at = stack.indexOfLast { it is DetailRoute && it.sessionId == sessionId }
        if (at >= 0) stack.subList(at + 1, stack.size).clear()
        else push(DetailRoute(sessionId))
    }

    /**
     * Pops the top screen. On the last one, replaces it with [orReplaceWith]
     * if given (a first-run settings screen hands over to the session list).
     */
    fun back(orReplaceWith: Route? = null) {
        when {
            stack.size > 1 -> stack.removeAt(stack.lastIndex)
            orReplaceWith != null -> stack[0] = orReplaceWith
        }
    }

    /** Drops everything above [anchor] and pushes [route] (if any) onto it. */
    fun replaceAbove(anchor: Route, route: Route?) {
        val at = stack.indexOf(anchor)
        if (at >= 0) stack.subList(at + 1, stack.size).clear()
        if (route != null) stack.add(route)
    }

    /** Replaces the open resident task (and what is above it) with [route]. */
    fun openResidentFromNotification(route: ResidentTaskRoute) {
        val at = stack.indexOfFirst { it is ResidentTaskRoute || it is ResidentFormRoute }
        if (at >= 0) stack.subList(at, stack.size).clear()
        stack.add(route)
    }

    /** Replaces the most recent session detail (and what is above it) with [route]. */
    fun openFromNotification(route: DetailRoute) {
        val at = stack.indexOfLast { it is DetailRoute }
        if (at >= 0) stack.subList(at, stack.size).clear()
        if (stack.lastOrNull() != route) stack.add(route)
    }
}
