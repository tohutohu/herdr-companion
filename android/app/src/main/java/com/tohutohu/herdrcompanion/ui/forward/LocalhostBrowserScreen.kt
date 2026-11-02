package com.tohutohu.herdrcompanion.ui.forward

import android.annotation.SuppressLint
import android.webkit.HttpAuthHandler
import android.webkit.WebChromeClient
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.navigationevent.NavigationEventInfo
import androidx.navigationevent.compose.NavigationBackHandler
import androidx.navigationevent.compose.rememberNavigationEventState
import androidx.webkit.ProxyConfig
import androidx.webkit.ProxyController
import androidx.webkit.WebViewFeature
import com.tohutohu.herdrcompanion.container
import com.tohutohu.herdrcompanion.ui.exceptBottom
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/**
 * Sends the app's WebView traffic for localhost, and nothing else, through the Gateway's proxy
 * while at least one localhost page is open. The override is process-wide, hence the count.
 */
private object LocalhostProxy {
    private var users = 0

    fun supported(): Boolean =
        WebViewFeature.isFeatureSupported(WebViewFeature.PROXY_OVERRIDE) &&
            WebViewFeature.isFeatureSupported(WebViewFeature.PROXY_OVERRIDE_REVERSE_BYPASS)

    @SuppressLint("RequiresFeature")
    fun acquire(context: android.content.Context, proxyRule: String, onReady: () -> Unit) {
        users++
        val config = ProxyConfig.Builder()
            .addProxyRule(proxyRule)
            // Reverse bypass: only these hosts go through the proxy. Loopback is normally never
            // proxied; removing the implicit rules lets it be.
            .setReverseBypassEnabled(true)
            .removeImplicitRules()
            .addBypassRule("localhost")
            .addBypassRule("127.0.0.1")
            .addBypassRule("[::1]")
            .build()
        ProxyController.getInstance().setProxyOverride(config, ContextCompat.getMainExecutor(context), onReady)
    }

    @SuppressLint("RequiresFeature")
    fun release(context: android.content.Context) {
        users--
        if (users == 0) ProxyController.getInstance().clearProxyOverride(ContextCompat.getMainExecutor(context)) {}
    }
}

/** A small browser for a server on the Mac's localhost, reached through the Gateway. */
@OptIn(ExperimentalMaterial3Api::class)
@SuppressLint("SetJavaScriptEnabled")
@Composable
fun LocalhostBrowserScreen(url: String, onBack: () -> Unit) {
    val context = LocalContext.current
    val gatewayUrl = context.container.settings.current.gatewayUrl
    val proxyRule = remember(gatewayUrl) { proxyRuleFor(gatewayUrl) }
    val proxyHost = remember(gatewayUrl) { gatewayUrl.trim().toHttpUrlOrNull()?.host }
    val supported = remember { LocalhostProxy.supported() }
    var ready by remember { mutableStateOf(false) }
    var progress by remember { mutableIntStateOf(0) }
    var title by remember { mutableStateOf("") }
    var current by remember { mutableStateOf(url) }
    var canGoBack by remember { mutableStateOf(false) }
    var webView by remember { mutableStateOf<WebView?>(null) }

    if (supported && proxyRule != null) {
        DisposableEffect(proxyRule) {
            LocalhostProxy.acquire(context, proxyRule) { ready = true }
            onDispose { LocalhostProxy.release(context) }
        }
    }
    NavigationBackHandler(
        state = rememberNavigationEventState(currentInfo = NavigationEventInfo.None),
        isBackEnabled = canGoBack,
        onBackCompleted = { webView?.goBack() },
    )

    Scaffold(
        topBar = {
            TopAppBar(
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
                },
                title = {
                    Column {
                        Text(title.ifBlank { displayUrl(current) }, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        if (title.isNotBlank()) {
                            Text(
                                displayUrl(current),
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                            )
                        }
                    }
                },
                actions = {
                    IconButton(onClick = { webView?.reload() }, enabled = webView != null) {
                        Icon(Icons.Default.Refresh, contentDescription = "Reload")
                    }
                },
            )
        },
    ) { padding ->
        Box(Modifier.padding(padding.exceptBottom()).consumeWindowInsets(padding).fillMaxSize()) {
            val problem = when {
                !supported -> "This phone's WebView cannot route localhost through the Gateway. Update Android System WebView."
                proxyRule == null -> "Opening the Mac's localhost needs a direct http:// connection to the Gateway (Tailscale). It does not work through the HTTPS tunnel."
                else -> null
            }
            if (problem != null) {
                Text(problem, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(16.dp))
                return@Box
            }
            if (ready) {
                AndroidView(
                    modifier = Modifier.fillMaxSize().padding(bottom = padding.calculateBottomPadding()),
                    factory = { ctx ->
                        WebView(ctx).apply {
                            settings.apply {
                                javaScriptEnabled = true
                                domStorageEnabled = true
                                allowFileAccess = false
                                allowContentAccess = false
                                useWideViewPort = true
                                loadWithOverviewMode = true
                                builtInZoomControls = true
                                displayZoomControls = false
                                // A dev server rebuilds between loads.
                                cacheMode = WebSettings.LOAD_NO_CACHE
                            }
                            webViewClient = ProxyAuthClient(
                                proxyHost = proxyHost,
                                token = { context.container.settings.current.token.trim() },
                                onPage = { u, back ->
                                    current = u
                                    canGoBack = back
                                },
                            )
                            webChromeClient = object : WebChromeClient() {
                                override fun onProgressChanged(view: WebView, newProgress: Int) {
                                    progress = newProgress
                                }

                                override fun onReceivedTitle(view: WebView, t: String?) {
                                    title = t.orEmpty()
                                }
                            }
                            loadUrl(url)
                            webView = this
                        }
                    },
                    onRelease = { it.destroy() },
                )
            }
            if (!ready || progress in 0..99) {
                LinearProgressIndicator(
                    progress = { if (ready) progress / 100f else 0f },
                    modifier = Modifier.fillMaxWidth().align(Alignment.TopCenter),
                )
            }
        }
    }
}

/** "localhost:5173/app" from "http://localhost:5173/app". */
private fun displayUrl(url: String): String = url.substringAfter("://").removeSuffix("/")

/** Answers the Gateway's proxy challenge with the paired token; any other site's is cancelled. */
private class ProxyAuthClient(
    private val proxyHost: String?,
    private val token: () -> String,
    private val onPage: (url: String, canGoBack: Boolean) -> Unit,
) : WebViewClient() {
    private var attempts = 0

    override fun onReceivedHttpAuthRequest(view: WebView, handler: HttpAuthHandler, host: String?, realm: String?) {
        val fromGateway = proxyHost != null && host?.substringBefore(':').equals(proxyHost, ignoreCase = true)
        // A token the Gateway keeps refusing would otherwise be offered forever.
        if (fromGateway && attempts++ < 3) {
            handler.proceed("herdr", token())
        } else {
            handler.cancel()
        }
    }

    override fun onPageFinished(view: WebView, url: String) {
        attempts = 0
    }

    override fun doUpdateVisitedHistory(view: WebView, url: String, isReload: Boolean) {
        onPage(url, view.canGoBack())
    }
}
