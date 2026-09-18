package com.tohutohu.herdrmobile

import android.app.Application
import coil3.ImageLoader
import coil3.PlatformContext
import coil3.SingletonImageLoader
import coil3.network.okhttp.OkHttpNetworkFetcherFactory
import com.tohutohu.herdrmobile.data.AgentPresetsStore
import com.tohutohu.herdrmobile.data.DirectoryShortcutsStore
import com.tohutohu.herdrmobile.data.FileDownloads
import com.tohutohu.herdrmobile.data.SessionRepository
import com.tohutohu.herdrmobile.data.SettingsStore
import com.tohutohu.herdrmobile.data.Settings
import com.tohutohu.herdrmobile.push.FirebaseRuntime
import com.google.firebase.FirebaseApp
import com.google.firebase.messaging.FirebaseMessaging
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.coroutines.tasks.await
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import com.tohutohu.herdrmobile.data.api.GatewayApi
import com.tohutohu.herdrmobile.data.db.AppDatabase
import com.tohutohu.herdrmobile.data.db.SessionEntity
import com.tohutohu.herdrmobile.push.Notifications
import com.tohutohu.herdrmobile.push.PushRegistration
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.stateIn
import okhttp3.OkHttpClient
import java.util.concurrent.TimeUnit

/** Manual dependency container; the app is small enough not to need DI. */
class AppContainer(private val app: Application) {
    val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    val settings = SettingsStore(app, scope)
    val directoryShortcuts = DirectoryShortcutsStore(app)
    val agentPresets = AgentPresetsStore(app)

    private val baseHttp = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(30, TimeUnit.SECONDS)
        .build()

    /** Adds the bearer token only for requests to the configured gateway. */
    val authedHttp: OkHttpClient = baseHttp.newBuilder()
        .addInterceptor { chain ->
            val req = chain.request()
            val s = settings.current
            val gateway = s.gatewayUrl.trim().toHttpUrlOrNull()
            val out = if (gateway != null && req.url.scheme == gateway.scheme && req.url.host == gateway.host && req.url.port == gateway.port && req.header("Authorization") == null) {
                req.newBuilder().header("Authorization", "Bearer ${s.token.trim()}").build()
            } else {
                req
            }
            chain.proceed(out)
        }
        .build()

    val api = GatewayApi(baseHttp) { settings.current }
    val db = AppDatabase.create(app)
    init {
        runBlocking {
            if (settings.needsCacheReset()) {
                withContext(Dispatchers.IO) { db.clearAllTables() }
                app.cacheDir.deleteRecursively()
                androidx.core.app.NotificationManagerCompat.from(app).cancelAll()
                settings.cacheResetComplete()
            }
        }
        FirebaseRuntime.initialize(app, settings.current)
    }
    val repository = SessionRepository(db, api)

    /**
     * The session list, kept while the app lives so a screen that comes back
     * renders the rows on its first frame (null until Room has answered once).
     */
    val sessions: StateFlow<List<SessionEntity>?> =
        repository.observeSessions().stateIn(scope, SharingStarted.WhileSubscribed(5_000), null)
    val pushRegistration = PushRegistration(app, api, scope)
    val downloads = FileDownloads(app, api, scope)

    /** Existing connections change only after a process restart, preventing mixed caches/jobs/FCM. */
    suspend fun configure(next: Settings): Boolean {
        if (next == settings.current) {
            pushRegistration.registerIfPossible()
            return false
        }
        val existingFirebase = FirebaseApp.getApps(app).firstOrNull { it.name == FirebaseApp.DEFAULT_APP_NAME }
        val firebaseChanged = existingFirebase != null && (next.firebase == null ||
            existingFirebase.options.applicationId != next.firebase.applicationId ||
            existingFirebase.options.apiKey != next.firebase.apiKey ||
            existingFirebase.options.projectId != next.firebase.projectId ||
            existingFirebase.options.gcmSenderId != next.firebase.senderId)
        if ((settings.current.isConfigured && next != settings.current) || firebaseChanged) {
            if (existingFirebase != null && settings.current.isConfigured) {
                val oldToken = withTimeoutOrNull(3_000) { runCatching { FirebaseMessaging.getInstance().token.await() }.getOrNull() }
                val previous = settings.current
                val oldApi = GatewayApi(baseHttp.newBuilder().callTimeout(3, TimeUnit.SECONDS).build()) { previous }
                if (oldToken != null) runCatching { oldApi.unregisterDevice(oldToken) }
            }
            settings.stage(next)
            return true
        }
        settings.save(next)
        FirebaseRuntime.initialize(app, next)
        pushRegistration.registerIfPossible()
        return false
    }
}

class HerdrMobileApp : Application(), SingletonImageLoader.Factory {
    lateinit var container: AppContainer
        private set

    override fun onCreate() {
        super.onCreate()
        container = AppContainer(this)
        Notifications.createChannels(this)
        container.pushRegistration.registerIfPossible()
    }

    override fun newImageLoader(context: PlatformContext): ImageLoader =
        ImageLoader.Builder(context)
            .components { add(OkHttpNetworkFetcherFactory(callFactory = { container.authedHttp })) }
            .build()
}

val android.content.Context.container: AppContainer
    get() = (applicationContext as HerdrMobileApp).container
