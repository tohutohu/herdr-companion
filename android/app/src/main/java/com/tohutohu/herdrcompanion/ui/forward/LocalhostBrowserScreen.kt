package com.tohutohu.herdrcompanion.ui.forward

import android.annotation.SuppressLint
import android.webkit.HttpAuthHandler
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebChromeClient
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.compose.foundation.background
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
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.saveable.rememberSaveable
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
import com.tohutohu.herdrcompanion.ui.files.CommentModeToggle
import com.tohutohu.herdrcompanion.ui.files.CommentablePage
import com.tohutohu.herdrcompanion.ui.files.PageComment
import com.tohutohu.herdrcompanion.ui.files.PendingPageComments
import com.tohutohu.herdrcompanion.ui.files.SyncCommentMarks
import com.tohutohu.herdrcompanion.ui.files.formatPageComments
import com.tohutohu.herdrcompanion.ui.files.injectCommentScript
import com.tohutohu.herdrcompanion.ui.files.installCommentBridge
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

/**
 * A small browser for a server on the Mac's localhost, reached through the Gateway. Opened from
 * [sessionId]'s conversation, its elements take comments that go into that session's draft.
 */
@OptIn(ExperimentalMaterial3Api::class)
@SuppressLint("SetJavaScriptEnabled")
@Composable
fun LocalhostBrowserScreen(
    url: String,
    sessionId: String?,
    onBack: () -> Unit,
    /** Back to the session, whose composer now holds the comments made here. */
    onOpenSession: () -> Unit = onBack,
) {
    val context = LocalContext.current
    val gatewayUrl = context.container.settings.current.gatewayUrl
    // An http:// Gateway is the proxy itself. Behind the HTTPS tunnel, which carries no proxy
    // requests, ask it for its direct address (the Tailscale IP).
    var proxyRule by remember(gatewayUrl) { mutableStateOf(proxyRuleFor(gatewayUrl)) }
    var lookedUp by remember(gatewayUrl) { mutableStateOf(proxyRule != null) }
    LaunchedEffect(gatewayUrl) {
        if (proxyRule != null) return@LaunchedEffect
        proxyRule = try {
            context.container.api.capabilities().localhostProxy?.let(::proxyRuleFor)
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
        } catch (_: Exception) {
            null
        }
        lookedUp = true
    }
    val proxyHost = proxyRule?.toHttpUrlOrNull()?.host
    var unreachable by remember { mutableStateOf(false) }
    val supported = remember { LocalhostProxy.supported() }
    var ready by remember { mutableStateOf(false) }
    var progress by remember { mutableIntStateOf(0) }
    var title by remember { mutableStateOf("") }
    var current by remember { mutableStateOf(url) }
    var canGoBack by remember { mutableStateOf(false) }
    var webView by remember { mutableStateOf<WebView?>(null) }
    // Counts loads that finished with the comment script in place.
    var pageLoads by remember { mutableIntStateOf(0) }
    var commentMode by rememberSaveable { mutableStateOf(false) }
    var comments by remember(sessionId, url) { mutableStateOf(sessionId?.let { PendingPageComments[it, url] }.orEmpty()) }
    fun updateComments(value: List<PageComment>) {
        comments = value
        if (sessionId != null) PendingPageComments[sessionId, url] = value
    }
    val shownPage = current.substringBefore('#').takeIf { pageLoads > 0 }

    val rule = proxyRule
    if (supported && rule != null) {
        DisposableEffect(rule) {
            LocalhostProxy.acquire(context, rule) { ready = true }
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
                    if (sessionId != null && ready) CommentModeToggle(commentMode) { commentMode = it }
                    IconButton(
                        onClick = {
                            unreachable = false
                            webView?.reload()
                        },
                        enabled = webView != null,
                    ) {
                        Icon(Icons.Default.Refresh, contentDescription = "Reload")
                    }
                },
            )
        },
    ) { padding ->
        Box(Modifier.padding(padding.exceptBottom()).consumeWindowInsets(padding).fillMaxSize()) {
            val problem = when {
                !supported -> "This phone's WebView cannot route localhost through the Gateway. Update Android System WebView."
                lookedUp && rule == null -> "Opening the Mac's localhost needs a direct http:// route to the Gateway (Tailscale), and this Gateway did not give one."
                else -> null
            }
            if (problem != null) {
                Text(problem, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(16.dp))
                return@Box
            }
            if (ready) {
                CommentablePage(
                    key = url,
                    bottom = padding.calculateBottomPadding(),
                    commentMode = commentMode,
                    comments = comments,
                    onCommentsChange = ::updateComments,
                    onCommentModeChange = { commentMode = it },
                    onSend = {
                        if (sessionId != null) context.container.composerDrafts.append(sessionId, formatPageComments(comments))
                        updateComments(emptyList())
                        commentMode = false
                        onOpenSession()
                    },
                ) { pageBottom, controller, onPick ->
                    val picking by rememberUpdatedState(commentMode)
                    val pick by rememberUpdatedState(onPick)
                    SyncCommentMarks(webView, shownPage, pageLoads, commentMode, comments)
                    AndroidView(
                        modifier = Modifier.fillMaxSize().padding(bottom = pageBottom),
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
                                installCommentBridge { element ->
                                    val page = this.url?.substringBefore('#')
                                    if (picking && page != null) pick(page, element)
                                }
                                webViewClient = ProxyAuthClient(
                                    proxyHost = proxyHost,
                                    token = { context.container.settings.current.token.trim() },
                                    onPage = { u, back ->
                                        current = u
                                        canGoBack = back
                                    },
                                    onLoaded = { view ->
                                        view.injectCommentScript()
                                        pageLoads++
                                    },
                                    onProxyUnreachable = { unreachable = true },
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
                                controller.webView = this
                            }
                        },
                        onRelease = {
                            if (controller.webView === it) controller.webView = null
                            it.destroy()
                        },
                    )
                }
            }
            if (unreachable) {
                Text(
                    "Could not reach the Gateway at ${proxyHost.orEmpty()} directly. Is Tailscale connected on this phone?",
                    color = MaterialTheme.colorScheme.onErrorContainer,
                    modifier = Modifier
                        .fillMaxWidth()
                        .background(MaterialTheme.colorScheme.errorContainer)
                        .padding(16.dp),
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
    private val onLoaded: (WebView) -> Unit,
    private val onProxyUnreachable: () -> Unit,
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

    // A server that is not running still answers (the Gateway's 502); failing to connect at all
    // means the proxy itself is out of reach.
    override fun onReceivedError(view: WebView, request: WebResourceRequest, error: WebResourceError) {
        if (request.isForMainFrame && error.errorCode in PROXY_ERRORS) onProxyUnreachable()
    }

    override fun onPageFinished(view: WebView, url: String) {
        attempts = 0
        onLoaded(view)
    }

    override fun doUpdateVisitedHistory(view: WebView, url: String, isReload: Boolean) {
        onPage(url, view.canGoBack())
    }
}

private val PROXY_ERRORS = setOf(
    WebViewClient.ERROR_CONNECT,
    WebViewClient.ERROR_HOST_LOOKUP,
    WebViewClient.ERROR_TIMEOUT,
    WebViewClient.ERROR_PROXY_AUTHENTICATION,
)
