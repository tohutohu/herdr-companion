package com.tohutohu.herdrcompanion.data.api

import java.io.IOException
import kotlinx.coroutines.suspendCancellableCoroutine
import okhttp3.Call
import okhttp3.Callback
import okhttp3.Response

/** Consumes and closes the response on OkHttp's IO thread; cancellation closes the call/socket. */
internal suspend fun <T> Call.awaitBody(consume: (Response) -> T): T = suspendCancellableCoroutine { continuation ->
    continuation.invokeOnCancellation { cancel() }
    enqueue(object : Callback {
        override fun onFailure(call: Call, e: IOException) {
            continuation.resumeWith(Result.failure(e))
        }

        override fun onResponse(call: Call, response: Response) {
            if (!continuation.isActive) {
                response.close()
                return
            }
            continuation.resumeWith(runCatching { response.use(consume) })
        }
    })
}
