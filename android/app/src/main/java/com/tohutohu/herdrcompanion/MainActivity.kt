package com.tohutohu.herdrcompanion

import android.Manifest
import android.content.Intent
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import com.tohutohu.herdrcompanion.data.SessionLink
import com.tohutohu.herdrcompanion.push.Notifications
import com.tohutohu.herdrcompanion.ui.AppNavigation
import com.tohutohu.herdrcompanion.ui.HerdrTheme
import com.tohutohu.herdrcompanion.ui.SessionsRoute
import com.tohutohu.herdrcompanion.ui.SettingsRoute
import kotlinx.coroutines.flow.MutableStateFlow

class MainActivity : ComponentActivity() {
    /** Session to open, set from notification taps and `hc://session/<id>` links. */
    private val pendingSession = MutableStateFlow<String?>(null)
    /** Resident task to open, set from notification taps. */
    private val pendingResident = MutableStateFlow<String?>(null)

    private val notificationPermission =
        registerForActivityResult(ActivityResultContracts.RequestPermission()) { }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        // The input bars draw their own surface down to the screen edge; the
        // translucent scrim three-button navigation would add cuts across it.
        window.isNavigationBarContrastEnforced = false
        // After a restore the saved back stack already reflects the launching
        // intent; the system hands it over again, but it must not re-navigate.
        if (savedInstanceState == null) handleIntent(intent)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
        val settings = container.settings

        setContent {
            val themeMode by settings.themeMode.collectAsState()
            HerdrTheme(themeMode) {
                val open by pendingSession.collectAsState()
                val openResident by pendingResident.collectAsState()
                AppNavigation(
                    start = if (settings.current.isConfigured) SessionsRoute else SettingsRoute,
                    openSession = open,
                    onSessionOpened = { pendingSession.value = null },
                    openResidentTask = openResident,
                    onResidentTaskOpened = { pendingResident.value = null },
                )
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        handleIntent(intent)
    }

    private fun handleIntent(intent: Intent?) {
        if (intent?.action == Intent.ACTION_VIEW) {
            // Any app or web page can send this, so it does nothing but navigate.
            val id = intent.dataString?.let(SessionLink::sessionId)
            intent.data = null
            if (id != null && container.settings.current.isConfigured) pendingSession.value = id
            return
        }
        if (intent?.getStringExtra("connection_id").orEmpty() != container.settings.current.connectionId) return
        intent?.getStringExtra(Notifications.EXTRA_RESIDENT_TASK)?.let { task ->
            intent.removeExtra(Notifications.EXTRA_RESIDENT_TASK)
            Notifications.cancelResident(this, task)
            pendingResident.value = task
            return
        }
        val id = intent?.getStringExtra(Notifications.EXTRA_SESSION_ID) ?: return
        intent.removeExtra(Notifications.EXTRA_SESSION_ID)
        Notifications.cancel(this, id)
        pendingSession.value = id
    }
}
