package com.tohutohu.herdrmobile.desktop

import java.nio.file.Files
import kotlin.test.Test
import kotlin.test.assertNotNull
import kotlin.test.assertNull

class DesktopInstanceLockTest {
    @Test
    fun `instance lock is exclusive and reusable after close`() {
        val home = Files.createTempDirectory("herdr-instance-lock")
        try {
            val first = DesktopInstanceLock.tryAcquire(home)
            assertNotNull(first)
            assertNull(DesktopInstanceLock.tryAcquire(home))

            first.close()

            val second = DesktopInstanceLock.tryAcquire(home)
            assertNotNull(second)
            second.close()
        } finally {
            home.toFile().deleteRecursively()
        }
    }
}
