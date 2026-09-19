package com.tohutohu.herdrmobile.ui

import androidx.compose.runtime.Composable
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
import com.tohutohu.herdrmobile.ui.detail.SessionDetailRoute
import com.tohutohu.herdrmobile.ui.files.FileViewerScreen
import com.tohutohu.herdrmobile.ui.files.ImageViewerScreen
import com.tohutohu.herdrmobile.ui.newsession.NewSessionScreen
import com.tohutohu.herdrmobile.ui.sessions.ArchivedSessionsScreen
import com.tohutohu.herdrmobile.ui.sessions.SessionListRoute
import com.tohutohu.herdrmobile.ui.settings.SettingsScreen
import com.tohutohu.herdrmobile.ui.terminal.TerminalScreen

/**
 * The app's screens, shown by Navigation 3 from a back stack that survives
 * configuration changes and process death.
 *
 * [openSession] is a session to bring up (from a notification tap); it
 * replaces any session detail already on the stack, so taps do not pile
 * screens up behind the one being opened.
 */
@Composable
fun AppNavigation(start: Route, openSession: String?, onSessionOpened: () -> Unit) {
    val backStack = rememberSerializable(serializer = NavBackStackSerializer(Route.serializer())) {
        NavBackStack(start)
    }
    val nav = remember(backStack) { Navigator(backStack) }
    LaunchedEffect(openSession) {
        openSession ?: return@LaunchedEffect
        nav.openFromNotification(DetailRoute(openSession, focusLatest = true))
        onSessionOpened()
    }
    val density = LocalDensity.current
    val motion = remember(density) { ScreenMotion(density) }

    NavDisplay(
        backStack = backStack,
        onBack = { nav.back() },
        entryDecorators = listOf(
            rememberSaveableStateHolderNavEntryDecorator(),
            // Scopes each screen's ViewModels to its entry, as NavBackStackEntry did.
            rememberViewModelStoreNavEntryDecorator(),
        ),
        transitionSpec = motion.push,
        popTransitionSpec = motion.pop,
        predictivePopTransitionSpec = motion.predictivePop,
        entryProvider = entryProvider {
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
                NewSessionScreen(
                    onBack = { lifecycle.ifResumed { nav.back() } },
                    onStarted = { id -> nav.replaceAbove(SessionsRoute, StartingRoute(id)) },
                )
            }
            entry<StartingRoute> { route ->
                com.tohutohu.herdrmobile.ui.newsession.StartingSessionScreen(
                    route.startId,
                    onOpenTerminal = { pane -> nav.push(TerminalRoute(paneId = pane)) },
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
                    onBack = { lifecycle.ifResumed { nav.back(orReplaceWith = SessionsRoute) } },
                    onOpenFile = { path, line -> lifecycle.ifResumed { nav.push(FileRoute(route.sessionId, path, line)) } },
                    onOpenImage = { url -> lifecycle.ifResumed { nav.push(ImageRoute(url)) } },
                    onOpenTerminal = { lifecycle.ifResumed { nav.push(TerminalRoute(route.sessionId)) } },
                )
            }
            entry<FileRoute> { route ->
                val lifecycle = LocalLifecycleOwner.current.lifecycle
                FileViewerScreen(route.sessionId, route.path, route.line, onBack = { lifecycle.ifResumed { nav.back() } })
            }
            entry<ImageRoute>(metadata = ScreenMotion.zoom) { route ->
                val lifecycle = LocalLifecycleOwner.current.lifecycle
                ImageViewerScreen(route.url, onBack = { lifecycle.ifResumed { nav.back() } })
            }
            entry<TerminalRoute> { route ->
                val lifecycle = LocalLifecycleOwner.current.lifecycle
                TerminalScreen(route.sessionId, onBack = { lifecycle.ifResumed { nav.back() } }, paneId = route.paneId)
            }
        },
    )
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
private class Navigator(private val stack: NavBackStack<Route>) {
    fun push(route: Route) {
        stack.add(route)
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

    /** Drops everything above [anchor] and pushes [route] onto it. */
    fun replaceAbove(anchor: Route, route: Route) {
        val at = stack.indexOf(anchor)
        if (at >= 0) stack.subList(at + 1, stack.size).clear()
        stack.add(route)
    }

    /** Replaces the most recent session detail (and what is above it) with [route]. */
    fun openFromNotification(route: DetailRoute) {
        val at = stack.indexOfLast { it is DetailRoute }
        if (at >= 0) stack.subList(at, stack.size).clear()
        if (stack.lastOrNull() != route) stack.add(route)
    }
}
