package com.tohutohu.herdrcompanion.ui

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.time.Instant

class BackgroundTasksTest {
    private val now = Instant.parse("2026-11-04T12:00:00Z")

    @Test
    fun `経過時間は大きい単位で丸めて示す`() {
        assertEquals("now", backgroundElapsed("2026-11-04T11:59:30Z", now))
        assertEquals("3m", backgroundElapsed("2026-11-04T11:57:00Z", now))
        assertEquals("2h", backgroundElapsed("2026-11-04T09:30:00Z", now))
        assertEquals("1d", backgroundElapsed("2026-11-03T11:00:00Z", now))
    }

    @Test
    fun `Gatewayが返す時差付きナノ秒の時刻を読める`() {
        assertEquals("3m", backgroundElapsed("2026-11-04T20:56:30.751639+09:00", now))
    }

    @Test
    fun `読めない開始時刻は経過時間を出さない`() {
        assertNull(backgroundElapsed("", now))
    }
}
