package com.tohutohu.herdrcompanion.desktop

import java.nio.channels.FileChannel
import java.nio.channels.FileLock
import java.nio.channels.OverlappingFileLockException
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.Paths
import java.nio.file.StandardOpenOption.CREATE
import java.nio.file.StandardOpenOption.WRITE

/**
 * Keeps one packaged Herdr UI process per user. The advisory lock is held by
 * the channel for the lifetime of the application, so a crashed process does
 * not leave a stale lock behind.
 */
internal class DesktopInstanceLock private constructor(
    private val channel: FileChannel,
    private val lock: FileLock,
) : AutoCloseable {
    override fun close() {
        runCatching { lock.release() }
        runCatching { channel.close() }
    }

    companion object {
        private const val BUNDLE_ID = "com.tohutohu.herdrcompanion.desktop"

        fun tryAcquire(home: Path = defaultHome()): DesktopInstanceLock? {
            val directory = home.resolve("Library/Application Support/Herdr")
            runCatching { Files.createDirectories(directory) }.getOrNull() ?: return null
            val channel = runCatching {
                FileChannel.open(directory.resolve("instance.lock"), CREATE, WRITE)
            }.getOrNull() ?: return null
            val lock = try {
                channel.tryLock()
            } catch (_: OverlappingFileLockException) {
                null
            }
            if (lock == null) {
                channel.close()
                return null
            }
            return DesktopInstanceLock(channel, lock)
        }

        fun activateExisting() {
            runCatching {
                ProcessBuilder("/usr/bin/open", "-b", BUNDLE_ID).start()
            }
        }

        private fun defaultHome(): Path = Paths.get(System.getProperty("user.home") ?: ".")
    }
}
