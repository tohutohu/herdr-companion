package com.tohutohu.herdrcompanion.ui

import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.List
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.Schedule
import androidx.compose.material3.Icon
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.navigation3.runtime.rememberDecoratedNavEntries
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSerializable
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.platform.UriHandler
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.viewmodel.navigation3.rememberViewModelStoreNavEntryDecorator
import androidx.navigation3.runtime.NavBackStack
import androidx.navigation3.runtime.entryProvider
import androidx.navigation3.runtime.rememberSaveableStateHolderNavEntryDecorator
import androidx.navigation3.runtime.serialization.NavBackStackSerializer
import androidx.navigation3.ui.NavDisplay
import com.tohutohu.herdrcompanion.data.SessionLink
import com.tohutohu.herdrcompanion.ui.detail.SessionDetailRoute
import com.tohutohu.herdrcompanion.ui.files.FileViewerScreen
import com.tohutohu.herdrcompanion.ui.forward.LocalhostBrowserScreen
import com.tohutohu.herdrcompanion.ui.forward.localhostTarget
import com.tohutohu.herdrcompanion.ui.files.ImageViewerRoute
import com.tohutohu.herdrcompanion.ui.resident.ResidentFormEntry
import com.tohutohu.herdrcompanion.ui.resident.ResidentHomeEntry
import com.tohutohu.herdrcompanion.ui.resident.ResidentTaskEntry
import com.tohutohu.herdrcompanion.ui.newsession.NewSessionRoute
import com.tohutohu.herdrcompanion.ui.projects.ProjectEntry
import com.tohutohu.herdrcompanion.ui.projects.ProjectsHomeEntry
import com.tohutohu.herdrcompanion.ui.sessions.ArchivedSessionsScreen
import com.tohutohu.herdrcompanion.ui.sessions.SessionListRoute
import com.tohutohu.herdrcompanion.ui.settings.SettingsScreen

/**
 * The app's screens, shown by Navigation 3 from back stacks that survive
 * configuration changes and process death. Sessions, 常駐 (resident tasks)
 * and Projects are top-level tabs, each with its own back stack, so
 * switching keeps where each one was. A session or a task opened from a
 * project stays in the Projects tab. The user leaves the app through
 * Sessions ("exit through home"): back from another tab's home returns to it.
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
    val projectStack = rememberSerializable(serializer = NavBackStackSerializer(Route.serializer())) {
        NavBackStack<Route>(ProjectsRoute)
    }
    var tab by rememberSaveable { mutableIntStateOf(TAB_SESSIONS) }
    val nav = remember(backStack) { Navigator(backStack) }
    val resident = remember(residentStack) { Navigator(residentStack) }
    val projects = remember(projectStack) { Navigator(projectStack) }
    // Screens that can be in more than one tab act on the tab shown.
    fun current(): Navigator = when (tab) {
        TAB_RESIDENT -> resident
        TAB_PROJECTS -> projects
        else -> nav
    }
    fun currentStack(): NavBackStack<Route> = when (tab) {
        TAB_RESIDENT -> residentStack
        TAB_PROJECTS -> projectStack
        else -> backStack
    }
    LaunchedEffect(openSession) {
        openSession ?: return@LaunchedEffect
        tab = TAB_SESSIONS
        nav.openFromNotification(DetailRoute(openSession, focusLatest = true))
        onSessionOpened()
    }
    LaunchedEffect(openResidentTask) {
        openResidentTask ?: return@LaunchedEffect
        tab = TAB_RESIDENT
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
                    onBack = { current().back(orReplaceWith = SessionsRoute.takeIf { tab == TAB_SESSIONS }) },
                    onReady = { id ->
                        for (stack in listOf(backStack, projectStack)) {
                            val at = stack.indexOf(route)
                            if (at >= 0) stack[at] = DetailRoute(id)
                        }
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
                    onOpenSession = { id -> lifecycle.ifResumed { current().openLinkedSession(id) } },
                    onBack = { lifecycle.ifResumed { current().back(orReplaceWith = SessionsRoute.takeIf { tab == TAB_SESSIONS }) } },
                    onOpenFile = { path, line -> lifecycle.ifResumed { current().push(FileRoute(route.sessionId, path, line)) } },
                    onOpenImage = { url -> lifecycle.ifResumed { current().push(ImageRoute(url)) } },
                )
            }
            entry<LocalhostRoute> { route ->
                val lifecycle = LocalLifecycleOwner.current.lifecycle
                LocalhostBrowserScreen(
                    url = route.url,
                    sessionId = route.sessionId,
                    onBack = { lifecycle.ifResumed { current().back() } },
                    onOpenSession = { route.sessionId?.let { id -> lifecycle.ifResumed { current().openLinkedSession(id) } } },
                )
            }
            entry<FileRoute> { route ->
                val lifecycle = LocalLifecycleOwner.current.lifecycle
                FileViewerScreen(
                    route.sessionId,
                    route.path,
                    route.line,
                    onBack = { lifecycle.ifResumed { current().back() } },
                    onOpenSession = { lifecycle.ifResumed { current().openLinkedSession(route.sessionId) } },
                )
            }
            entry<ImageRoute>(metadata = ScreenMotion.zoom) { route ->
                val lifecycle = LocalLifecycleOwner.current.lifecycle
                ImageViewerRoute(route.url, onBack = { lifecycle.ifResumed { current().back() } })
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
                    onBack = { lifecycle.ifResumed { current().back() } },
                    onEdit = { id -> lifecycle.ifResumed { current().push(ResidentFormRoute(id)) } },
                    onDeleted = { if (tab == TAB_RESIDENT) resident.replaceAbove(ResidentHomeRoute, null) else current().back() },
                    // A run continued as a session opens in the Sessions tab
                    // (the 常駐 tab keeps the task), or on top of it in a project.
                    onOpenSession = { id ->
                        if (tab == TAB_PROJECTS) {
                            projects.push(DetailRoute(id, focusLatest = true))
                        } else {
                            tab = TAB_SESSIONS
                            nav.openFromNotification(DetailRoute(id, focusLatest = true))
                        }
                    },
                )
            }
            entry<ResidentFormRoute> { route ->
                val lifecycle = LocalLifecycleOwner.current.lifecycle
                ResidentFormEntry(
                    taskId = route.taskId,
                    initialCwd = route.cwd,
                    onSaved = { id ->
                        when {
                            route.taskId != null -> current().back()
                            tab == TAB_RESIDENT -> resident.replaceAbove(ResidentHomeRoute, ResidentTaskRoute(id))
                            else -> {
                                current().back()
                                current().push(ResidentTaskRoute(id))
                            }
                        }
                    },
                    onCancel = { lifecycle.ifResumed { current().back() } },
                )
            }
            entry<ProjectsRoute> {
                val lifecycle = LocalLifecycleOwner.current.lifecycle
                ProjectsHomeEntry(onOpen = { key -> lifecycle.ifResumed { projects.push(ProjectRoute(key)) } })
            }
            entry<ProjectRoute> { route ->
                val lifecycle = LocalLifecycleOwner.current.lifecycle
                ProjectEntry(
                    key = route.key,
                    onBack = { lifecycle.ifResumed { projects.back() } },
                    onOpenSession = { id -> lifecycle.ifResumed { projects.push(DetailRoute(id)) } },
                    onOpenTask = { id -> lifecycle.ifResumed { projects.push(ResidentTaskRoute(id)) } },
                    onNewSession = { cwd -> lifecycle.ifResumed { projects.push(ProjectNewSessionRoute(cwd)) } },
                    onNewTask = { cwd -> lifecycle.ifResumed { projects.push(ResidentFormRoute(cwd = cwd)) } },
                )
            }
            entry<ProjectNewSessionRoute> { route ->
                val lifecycle = LocalLifecycleOwner.current.lifecycle
                NewSessionRoute(
                    onBack = { lifecycle.ifResumed { projects.back() } },
                    onStarted = { id ->
                        projects.back()
                        projects.push(StartingRoute(id))
                    },
                    initialCwd = route.cwd,
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
    val projectEntries = rememberDecoratedNavEntries(
        backStack = projectStack,
        entryDecorators = listOf(rememberSaveableStateHolderNavEntryDecorator(), rememberViewModelStoreNavEntryDecorator()),
        entryProvider = provider,
    )
    val top = currentStack().lastOrNull()
    val showTabs = top == SessionsRoute || top == ResidentHomeRoute || top == ProjectsRoute
    Scaffold(
        contentWindowInsets = WindowInsets(0, 0, 0, 0),
        bottomBar = {
            if (showTabs) {
                NavigationBar {
                    NavigationBarItem(
                        selected = tab == TAB_SESSIONS,
                        onClick = { tab = TAB_SESSIONS },
                        icon = { Icon(Icons.AutoMirrored.Filled.List, contentDescription = null) },
                        label = { Text("Sessions") },
                    )
                    NavigationBarItem(
                        selected = tab == TAB_RESIDENT,
                        onClick = { tab = TAB_RESIDENT },
                        icon = { Icon(Icons.Default.Schedule, contentDescription = null) },
                        label = { Text("常駐") },
                    )
                    NavigationBarItem(
                        selected = tab == TAB_PROJECTS,
                        onClick = { tab = TAB_PROJECTS },
                        icon = { Icon(Icons.Default.Folder, contentDescription = null) },
                        label = { Text("Projects") },
                    )
                }
            }
        },
    ) { padding ->
        // A localhost link in a message means the Mac's localhost: open it through the Gateway.
        val defaultUris = LocalUriHandler.current
        val uris = remember(defaultUris) {
            object : UriHandler {
                override fun openUri(uri: String) {
                    // A session link in a message (hc://session/<id>) opens that session here.
                    SessionLink.sessionId(uri)?.let { id -> current().push(DetailRoute(id)); return }
                    val local = localhostTarget(uri)
                    // Comments on the page go to the session the link was in.
                    val session = currentStack().lastOrNull { it is DetailRoute } as DetailRoute?
                    if (local != null) current().push(LocalhostRoute(local, session?.sessionId)) else defaultUris.openUri(uri)
                }
            }
        }
        CompositionLocalProvider(LocalUriHandler provides uris) {
            NavDisplay(
                entries = when (tab) {
                    TAB_RESIDENT -> sessionEntries + residentEntries
                    TAB_PROJECTS -> sessionEntries + projectEntries
                    else -> sessionEntries
                },
                modifier = Modifier.padding(padding).consumeWindowInsets(padding),
                onBack = {
                    when {
                        tab == TAB_SESSIONS -> nav.back()
                        currentStack().size > 1 -> current().back()
                        else -> tab = TAB_SESSIONS
                    }
                },
                transitionSpec = motion.push,
                popTransitionSpec = motion.pop,
                predictivePopTransitionSpec = motion.predictivePop,
            )
        }
    }
}

private const val TAB_SESSIONS = 0
private const val TAB_RESIDENT = 1
private const val TAB_PROJECTS = 2

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
