package com.tohutohu.herdrmobile

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
import com.tohutohu.herdrmobile.push.Notifications
import com.tohutohu.herdrmobile.ui.AppNavigation
import com.tohutohu.herdrmobile.ui.HerdrTheme
import com.tohutohu.herdrmobile.ui.SessionsRoute
import com.tohutohu.herdrmobile.ui.SettingsRoute
import kotlinx.coroutines.flow.MutableStateFlow

class MainActivity : ComponentActivity() {
    /** Session to open, set from notification taps. */
    private val pendingSession = MutableStateFlow<String?>(null)

    private val notificationPermission =
        registerForActivityResult(ActivityResultContracts.RequestPermission()) { }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        // After a restore the saved back stack already reflects the launching
        // intent; the system hands it over again, but it must not re-navigate.
        if (savedInstanceState == null) handleIntent(intent)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
        val settings = container.settings

        setContent {
            HerdrTheme {
                val open by pendingSession.collectAsState()
                AppNavigation(
                    start = if (settings.current.isConfigured) SessionsRoute else SettingsRoute,
                    openSession = open,
                    onSessionOpened = { pendingSession.value = null },
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
        if (intent?.getStringExtra("connection_id").orEmpty() != container.settings.current.connectionId) return
        val id = intent?.getStringExtra(Notifications.EXTRA_SESSION_ID) ?: return
        intent.removeExtra(Notifications.EXTRA_SESSION_ID)
        Notifications.cancel(this, id)
        pendingSession.value = id
    }
}
