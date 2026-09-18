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
class AppContainer(app: Application) {
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
            val gateway = s.gatewayUrl.trim().trimEnd('/')
            val out = if (gateway.isNotEmpty() && req.url.toString().startsWith(gateway) && req.header("Authorization") == null) {
                req.newBuilder().header("Authorization", "Bearer ${s.token.trim()}").build()
            } else {
                req
            }
            chain.proceed(out)
        }
        .build()

    val api = GatewayApi(baseHttp) { settings.current }
    val db = AppDatabase.create(app)
    val repository = SessionRepository(db, api)

    /**
     * The session list, kept while the app lives so a screen that comes back
     * renders the rows on its first frame (null until Room has answered once).
     */
    val sessions: StateFlow<List<SessionEntity>?> =
        repository.observeSessions().stateIn(scope, SharingStarted.WhileSubscribed(5_000), null)
    val pushRegistration = PushRegistration(app, api, scope)
    val downloads = FileDownloads(app, api, scope)
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
