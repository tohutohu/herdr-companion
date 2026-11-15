package com.tohutohu.herdrcompanion.data

import com.tohutohu.herdrcompanion.data.api.FavoriteDirectoriesDto
import com.tohutohu.herdrcompanion.data.api.GatewayApi
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext

/**
 * The favorite folders are the gateway's (`/v1/directories/favorites`), so the
 * apps and the agents driving the gateway share them. The device keeps the
 * last list it received as a cache: New Session shows it at once, and each
 * answer from the gateway replaces it. A change shows first on the device and
 * is undone when the gateway refuses it.
 *
 * The first time a device reaches a gateway that never saved a list, it
 * moves the favorites it kept on its own over. A gateway too old to keep
 * favorites leaves them on the device, as before.
 */
class FavoriteDirectoriesSync(
    private val api: GatewayApi,
    private val cache: Cache,
) {
    interface Cache {
        suspend fun read(): List<String>
        suspend fun write(paths: List<String>)
    }

    private val mutex = Mutex()

    /** Whether the gateway keeps favorites; null until it answered once. */
    private var gatewayKeepsThem: Boolean? = null

    /** Replaces the cache with the gateway's list and returns it. */
    suspend fun refresh(): List<String> = mutex.withLock {
        syncLocked(cache.read()).also { cache.write(it) }
    }

    suspend fun toggle(path: String): List<String> = change({ toggleFavorite(it, path) }) { before ->
        if (path in before) api.removeFavoriteDirectory(path) else api.addFavoriteDirectory(path)
    }

    /**
     * Puts the favorites in [paths]' order. One added or removed elsewhere
     * since the screen showed the list is kept added or removed.
     */
    suspend fun reorder(paths: List<String>): List<String> =
        change({ reordered(it, paths) }) { api.setFavoriteDirectories(reordered(it, paths)) }

    /** The gateway's list, after moving [local] over if it never saved one. */
    private suspend fun syncLocked(local: List<String>): List<String> {
        val remote = api.favoriteDirectories()
        gatewayKeepsThem = remote != null
        if (remote == null) return local
        return if (!remote.saved && local.isNotEmpty()) api.importFavoriteDirectories(local).paths else remote.paths
    }

    private suspend fun change(
        edit: (List<String>) -> List<String>,
        send: suspend (before: List<String>) -> FavoriteDirectoriesDto,
    ): List<String> = mutex.withLock {
        val cached = cache.read()
        cache.write(edit(cached))
        try {
            // Before this device first synced, the gateway's list (or the
            // device's own, moved over) is what the change applies to.
            val before = if (gatewayKeepsThem == null) syncLocked(cached) else cached
            val after = if (gatewayKeepsThem == true) send(before).paths else edit(before)
            cache.write(after)
            after
        } catch (error: Throwable) {
            withContext(NonCancellable) { cache.write(cached) }
            throw error
        }
    }
}

internal fun reordered(current: List<String>, order: List<String>): List<String> =
    order.filter { it in current } + current.filter { it !in order }
