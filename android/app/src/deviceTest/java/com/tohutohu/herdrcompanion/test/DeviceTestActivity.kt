package com.tohutohu.herdrcompanion.test

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import com.tohutohu.herdrcompanion.BuildConfig

/** A real Activity/Bundle host, present only in the isolated device-test APK. */
class DeviceTestActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        check(BuildConfig.APPLICATION_ID.endsWith(".devicetest"))
        setContent { MaterialTheme { content() } }
    }

    companion object {
        var content: @Composable () -> Unit = {}
    }
}
