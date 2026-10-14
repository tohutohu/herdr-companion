package com.tohutohu.herdrcompanion.desktop

import com.tohutohu.herdrcompanion.data.api.GatewayApi
import okhttp3.OkHttpClient
import java.awt.Rectangle
import java.nio.file.Files
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class DesktopDetachedWindowTest {
    private val saved = mutableListOf<DesktopLayoutSnapshot>()
    private val state = run {
        // An empty home and a closed port keep the test away from the real Gateway.
        val source = DesktopConnectionSource(
            home = Files.createTempDirectory("detached-window-test"),
            environment = emptyMap(),
            properties = mapOf("herdr.gateway.url" to "http://127.0.0.1:9"),
        )
        val http = OkHttpClient()
        DesktopAppState(
            connectionSource = source,
            api = GatewayApi(http) { source.current.settings },
            http = http,
            onLayoutChanged = { saved += it },
        )
    }

    @AfterTest
    fun tearDown() = state.close()

    private fun detachedIds() = state.detachedWindows.map { it.sessionId }

    @Test
    fun `ペインを別ウィンドウに移すと分割表示から外れて位置と一緒に保存される`() {
        state.openSession("one")
        state.openSessionInSplit("two")

        state.openSessionInWindow("one", x = 100, y = 200)

        assertEquals(listOf("two"), state.openSessionIds)
        assertEquals(listOf("one"), detachedIds())
        assertEquals("two", state.selectedSessionId)
        val layout = saved.last()
        assertEquals(listOf("two"), layout.openSessionIds)
        assertEquals(listOf(DesktopDetachedWindow("one", x = 100, y = 200)), layout.detachedWindows)
    }

    @Test
    fun `別ウィンドウのセッションを開き直すとペインを増やさずウィンドウを前面に出す`() {
        state.openSessionInWindow("one")

        state.openSession("one")
        state.openSessionInSplit("one")
        state.openSessionInWindow("one")

        assertEquals(emptyList(), state.openSessionIds)
        assertEquals(listOf("one"), detachedIds())
        assertEquals("one", state.windowRaiseRequest?.first)
    }

    @Test
    fun `別ウィンドウを閉じるとセッションも閉じる`() {
        state.openSessionInWindow("one")

        state.closeSession("one")

        assertEquals(emptyList(), detachedIds())
        assertEquals(emptyList(), saved.last().detachedWindows)
        assertNull(state.detailFor("one"))
    }

    @Test
    fun `メインウィンドウに戻すと分割ペインの末尾に入ってフォーカスされる`() {
        state.openSession("one")
        state.openSessionInWindow("two")

        state.returnToMainWindow("two")

        assertEquals(listOf("one", "two"), state.openSessionIds)
        assertEquals(emptyList(), detachedIds())
        assertEquals("two", state.selectedSessionId)
    }

    @Test
    fun `ウィンドウを動かすと位置と大きさが保存される`() {
        state.openSessionInWindow("one")

        state.updateDetachedBounds("one", x = 10, y = 20, width = 600, height = 700)

        assertEquals(
            listOf(DesktopDetachedWindow("one", x = 10, y = 20, width = 600, height = 700)),
            saved.last().detachedWindows,
        )
    }

    @Test
    fun `起動時に別ウィンドウも復元し画面外の位置は捨てる`() {
        state.restoreLayout(
            DesktopLayoutSnapshot(
                openSessionIds = listOf("one"),
                detachedWindows = listOf(
                    DesktopDetachedWindow("two", x = 100, y = 100),
                    DesktopDetachedWindow("three", x = 9_000, y = 9_000),
                ),
            ),
            screens = listOf(Rectangle(0, 0, 1_920, 1_080)),
        )

        assertEquals(listOf("one"), state.openSessionIds)
        assertEquals(
            listOf(DesktopDetachedWindow("two", x = 100, y = 100), DesktopDetachedWindow("three")),
            state.detachedWindows,
        )
    }
}
