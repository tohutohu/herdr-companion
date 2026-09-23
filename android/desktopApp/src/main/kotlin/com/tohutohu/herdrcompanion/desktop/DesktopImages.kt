package com.tohutohu.herdrcompanion.desktop

import coil3.ImageLoader
import coil3.PlatformContext
import coil3.network.okhttp.OkHttpNetworkFetcherFactory
import com.tohutohu.herdrcompanion.data.Settings
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.Interceptor
import okhttp3.OkHttpClient

/**
 * Image loader for message images, which the Gateway serves only with the
 * bearer token. Naming the network fetcher here also keeps it in the release
 * build: ProGuard cannot see Coil's ServiceLoader registration and strips it.
 */
fun desktopImageLoader(context: PlatformContext, http: OkHttpClient, settings: () -> Settings): ImageLoader {
    val authed = http.newBuilder().addInterceptor(gatewayAuthInterceptor(settings)).build()
    return ImageLoader.Builder(context)
        .components { add(OkHttpNetworkFetcherFactory(callFactory = { authed })) }
        .build()
}

/** Adds the bearer token only for requests to the configured Gateway. */
internal fun gatewayAuthInterceptor(settings: () -> Settings) = Interceptor { chain ->
    val request = chain.request()
    val current = settings()
    val gateway = current.gatewayUrl.trim().toHttpUrlOrNull()
    val sameOrigin = gateway != null && request.url.scheme == gateway.scheme &&
        request.url.host == gateway.host && request.url.port == gateway.port
    chain.proceed(
        if (sameOrigin && request.header("Authorization") == null) {
            request.newBuilder().header("Authorization", "Bearer ${current.token.trim()}").build()
        } else {
            request
        },
    )
}
