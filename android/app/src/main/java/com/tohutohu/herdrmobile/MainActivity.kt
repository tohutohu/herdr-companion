package com.tohutohu.herdrmobile

import android.Manifest
import android.content.Intent
import android.os.Build
import android.os.Bundle
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.toRoute
import com.tohutohu.herdrmobile.push.Notifications
import com.tohutohu.herdrmobile.ui.DetailRoute
import com.tohutohu.herdrmobile.ui.FileRoute
import com.tohutohu.herdrmobile.ui.HerdrTheme
import com.tohutohu.herdrmobile.ui.ImageRoute
import com.tohutohu.herdrmobile.ui.NewSessionRoute
import com.tohutohu.herdrmobile.ui.newsession.NewSessionScreen
import com.tohutohu.herdrmobile.ui.SessionsRoute
import com.tohutohu.herdrmobile.ui.SettingsRoute
import com.tohutohu.herdrmobile.ui.TerminalRoute
import com.tohutohu.herdrmobile.ui.detail.SessionDetailScreen
import com.tohutohu.herdrmobile.ui.files.FileViewerScreen
import com.tohutohu.herdrmobile.ui.files.ImageViewerScreen
import com.tohutohu.herdrmobile.ui.sessions.SessionListScreen
import com.tohutohu.herdrmobile.ui.settings.SettingsScreen
import com.tohutohu.herdrmobile.ui.terminal.TerminalScreen
import com.tohutohu.herdrmobile.data.Settings
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.runBlocking

class MainActivity : ComponentActivity() {
    /** Session to open, set from notification taps. */
    private val pendingSession = MutableStateFlow<String?>(null)

    private val notificationPermission =
        registerForActivityResult(ActivityResultContracts.RequestPermission()) { }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        handleIntent(intent)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
        val settings = container.settings

        setContent {
            HerdrTheme {
                val nav = rememberNavController()
                val open by pendingSession.collectAsState()
                LaunchedEffect(open) {
                    val id = open ?: return@LaunchedEffect
                    nav.navigate(DetailRoute(id)) { launchSingleTop = true }
                    pendingSession.value = null
                }
                val start: Any = if (settings.current.isConfigured) SessionsRoute else SettingsRoute
                NavHost(navController = nav, startDestination = start) {
                    composable<SessionsRoute> {
                        SessionListScreen(
                            onOpen = { nav.navigate(DetailRoute(it)) },
                            onSettings = { nav.navigate(SettingsRoute) },
                            onNew = { nav.navigate(NewSessionRoute) },
                        )
                    }
                    composable<NewSessionRoute> {
                        NewSessionScreen(
                            onBack = { nav.popBackStack() },
                            onStarted = { sessionId, warning ->
                                warning?.let { Toast.makeText(this@MainActivity, it, Toast.LENGTH_LONG).show() }
                                if (sessionId != null) {
                                    nav.navigate(DetailRoute(sessionId)) { popUpTo(SessionsRoute) }
                                } else {
                                    nav.popBackStack()
                                }
                            },
                        )
                    }
                    composable<SettingsRoute> {
                        SettingsScreen(onDone = {
                            if (!nav.popBackStack()) {
                                nav.navigate(SessionsRoute) { popUpTo(SettingsRoute) { inclusive = true } }
                            }
                        })
                    }
                    composable<DetailRoute> { entry ->
                        val route = entry.toRoute<DetailRoute>()
                        SessionDetailScreen(
                            sessionId = route.sessionId,
                            onBack = {
                                if (!nav.popBackStack()) nav.navigate(SessionsRoute)
                            },
                            onOpenFile = { path, line -> nav.navigate(FileRoute(route.sessionId, path, line)) },
                            onOpenImage = { url -> nav.navigate(ImageRoute(url)) },
                            onOpenTerminal = { nav.navigate(TerminalRoute(route.sessionId)) },
                        )
                    }
                    composable<FileRoute> { entry ->
                        val route = entry.toRoute<FileRoute>()
                        FileViewerScreen(route.sessionId, route.path, route.line, onBack = { nav.popBackStack() })
                    }
                    composable<ImageRoute> { entry ->
                        ImageViewerScreen(entry.toRoute<ImageRoute>().url, onBack = { nav.popBackStack() })
                    }
                    composable<TerminalRoute> { entry ->
                        TerminalScreen(entry.toRoute<TerminalRoute>().sessionId, onBack = { nav.popBackStack() })
                    }
                }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handleIntent(intent)
    }

    private fun handleIntent(intent: Intent?) {
        applyDebugSettings(intent)
        val id = intent?.getStringExtra(Notifications.EXTRA_SESSION_ID) ?: return
        intent.removeExtra(Notifications.EXTRA_SESSION_ID)
        Notifications.cancel(this, id)
        pendingSession.value = id
    }

    /**
     * Debug builds only: `adb shell am start -n com.tohutohu.herdrmobile/.MainActivity
     * --es gateway_url http://host:8765 --es token XXX` configures the app
     * without typing (release builds ignore these extras).
     */
    private fun applyDebugSettings(intent: Intent?) {
        if (!BuildConfig.DEBUG || intent == null) return
        val url = intent.getStringExtra("gateway_url") ?: return
        val token = intent.getStringExtra("token") ?: return
        intent.removeExtra("token")
        runBlocking { container.settings.save(Settings(url, token)) }
        container.pushRegistration.registerIfPossible()
    }
}
