package com.tohutohu.herdrcompanion.ui.files

import android.annotation.SuppressLint
import android.content.ActivityNotFoundException
import android.content.Intent
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.navigationevent.NavigationEventInfo
import androidx.navigationevent.compose.NavigationBackHandler
import androidx.navigationevent.compose.rememberNavigationEventState
import com.tohutohu.herdrcompanion.container
import com.tohutohu.herdrcompanion.data.FileDownloads
import com.tohutohu.herdrcompanion.data.api.GatewayApi
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.OkHttpClient
import java.io.ByteArrayInputStream

/** A made-up origin: requests to it never leave the app and are answered from the gateway. */
internal const val HTML_PAGE_HOST = "files.herdr-companion.invalid"

/**
 * The page URL for a gateway file path. Absolute and root-relative paths keep separate prefixes
 * so a relative link (`style.css`, `../img/a.png`) resolves next to the page as it would on disk.
 */
internal fun htmlPageUrl(path: String): String {
    val absolute = path.startsWith("/")
    return HttpUrl.Builder()
        .scheme("https")
        .host(HTML_PAGE_HOST)
        .addPathSegment(if (absolute) "a" else "r")
        .addPathSegments(path.trimStart('/'))
        .build()
        .toString()
}

/** The gateway file path a page URL stands for, or null for any other URL. */
internal fun gatewayPathForPageUrl(url: String): String? {
    val parsed = url.toHttpUrlOrNull() ?: return null
    if (parsed.host != HTML_PAGE_HOST) return null
    val segments = parsed.pathSegments
    if (segments.size < 2 || segments.drop(1).any { it.isEmpty() }) return null
    val rest = segments.drop(1).joinToString("/")
    return when (segments.first()) {
        "a" -> "/$rest"
        "r" -> rest
        else -> null
    }
}

/**
 * The gateway serves every text file as text/plain so nothing renders by accident; the page needs
 * the real type of each stylesheet, script and image.
 */
internal fun htmlResourceMimeType(path: String, served: String?): String {
    val ext = path.substringAfterLast('/').substringAfterLast('.', "").lowercase()
    return when (ext) {
        "htm", "html" -> "text/html"
        "css" -> "text/css"
        "js", "mjs" -> "text/javascript"
        "json", "map" -> "application/json"
        "svg" -> "image/svg+xml"
        "wasm" -> "application/wasm"
        else -> FileDownloads.mimeTypeFor(path).takeIf { it != "application/octet-stream" }
            ?: served?.substringBefore(';')?.trim()?.takeIf { it.isNotEmpty() }
            ?: "application/octet-stream"
    }
}

/** Renders an HTML file from the session's workspace, with its relative CSS, scripts and images. */
@SuppressLint("SetJavaScriptEnabled")
@Composable
internal fun HtmlPageView(sessionId: String, path: String, bottom: Dp) {
    val context = LocalContext.current
    val api = context.container.api
    val http = context.container.authedHttp
    var progress by remember(sessionId, path) { mutableIntStateOf(0) }
    var canGoBack by remember(sessionId, path) { mutableStateOf(false) }
    var webView by remember(sessionId, path) { mutableStateOf<WebView?>(null) }

    NavigationBackHandler(
        state = rememberNavigationEventState(currentInfo = NavigationEventInfo.None),
        isBackEnabled = canGoBack,
        onBackCompleted = { webView?.goBack() },
    )

    Box(Modifier.fillMaxSize().padding(bottom = bottom)) {
        AndroidView(
            modifier = Modifier.fillMaxSize(),
            factory = { ctx ->
                WebView(ctx).apply {
                    settings.apply {
                        // Generated reports often draw their charts with scripts.
                        javaScriptEnabled = true
                        domStorageEnabled = true
                        allowFileAccess = false
                        allowContentAccess = false
                        // The agent may rewrite the file between opens.
                        cacheMode = WebSettings.LOAD_NO_CACHE
                        useWideViewPort = true
                        loadWithOverviewMode = true
                        builtInZoomControls = true
                        displayZoomControls = false
                    }
                    webViewClient = WorkspacePageClient(sessionId, api, http) { canGoBack = it }
                    webChromeClient = object : android.webkit.WebChromeClient() {
                        override fun onProgressChanged(view: WebView, newProgress: Int) {
                            progress = newProgress
                        }
                    }
                    loadUrl(htmlPageUrl(path))
                    webView = this
                }
            },
            onRelease = { it.destroy() },
        )
        if (progress in 0..99) {
            LinearProgressIndicator(
                progress = { progress / 100f },
                modifier = Modifier.fillMaxWidth().align(Alignment.TopCenter),
            )
        }
    }
}

private class WorkspacePageClient(
    private val sessionId: String,
    private val api: GatewayApi,
    private val http: OkHttpClient,
    private val onHistoryChanged: (canGoBack: Boolean) -> Unit,
) : WebViewClient() {
    override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest): WebResourceResponse? {
        val url = request.url.toString()
        if (request.url.host != HTML_PAGE_HOST) return null
        val path = gatewayPathForPageUrl(url)
        if (path == null || request.method != "GET") return failure(404, "Not Found")
        return try {
            val resp = http.newCall(api.mediaRequest(sessionId, path)).execute()
            if (!resp.isSuccessful) {
                resp.close()
                return failure(resp.code, resp.message.ifBlank { "Error" })
            }
            val mime = htmlResourceMimeType(path, resp.header("Content-Type"))
            val charset = if (mime.startsWith("text/") || mime == "application/json" || mime == "image/svg+xml") "utf-8" else null
            WebResourceResponse(mime, charset, 200, "OK", mapOf("Cache-Control" to "no-store"), resp.body.byteStream())
        } catch (e: Exception) {
            failure(502, "Bad Gateway")
        }
    }

    override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
        val uri = request.url
        if (uri.host == HTML_PAGE_HOST) return false
        // Anything outside the workspace belongs in a real browser.
        if (uri.scheme in setOf("http", "https", "mailto")) {
            try {
                view.context.startActivity(Intent(Intent.ACTION_VIEW, uri).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            } catch (_: ActivityNotFoundException) {
            }
        }
        return true
    }

    override fun doUpdateVisitedHistory(view: WebView, url: String?, isReload: Boolean) {
        onHistoryChanged(view.canGoBack())
    }

    private fun failure(code: Int, reason: String) =
        WebResourceResponse("text/plain", "utf-8", code, reason, emptyMap(), ByteArrayInputStream(ByteArray(0)))
}
