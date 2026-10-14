package com.tohutohu.herdrcompanion.desktop

import kotlin.test.Test
import kotlin.test.assertEquals

class DesktopLayoutSnapshotTest {
    @Test
    fun `開いていたペインの順序とフォーカスと幅を保存して復元できる`() {
        val layout = DesktopLayoutSnapshot(
            openSessionIds = listOf("claude:one", "codex:two"),
            focusedSessionId = "claude:one",
            paneWeights = listOf(1.4f, 0.6f),
        )

        assertEquals(layout, decodeLayout(encodeLayout(layout)))
    }

    @Test
    fun `保存値が無いか壊れていればペインを開かずに起動する`() {
        assertEquals(DesktopLayoutSnapshot(), decodeLayout(null))
        assertEquals(DesktopLayoutSnapshot(), decodeLayout("not json"))
    }

    @Test
    fun `フォーカス先が開いていなければ最後のペインにフォーカスする`() {
        val restored = DesktopLayoutSnapshot(
            openSessionIds = listOf("one", "two", "one", ""),
            focusedSessionId = "gone",
        ).normalized()

        assertEquals(listOf("one", "two"), restored.openSessionIds)
        assertEquals("two", restored.focusedSessionId)
    }

    @Test
    fun `ペイン数と合わないか不正な幅は均等幅に戻す`() {
        val ids = listOf("one", "two")

        assertEquals(
            listOf(1f, 1f),
            DesktopLayoutSnapshot(ids, paneWeights = listOf(1f)).normalized().paneWeights,
        )
        assertEquals(
            listOf(1f, 1f),
            DesktopLayoutSnapshot(ids, paneWeights = listOf(1f, Float.NaN)).normalized().paneWeights,
        )
        assertEquals(
            listOf(1f, 1f),
            DesktopLayoutSnapshot(ids, paneWeights = listOf(1f, 0f)).normalized().paneWeights,
        )
    }

    @Test
    fun `別ウィンドウの位置と大きさを保存して復元できる`() {
        val layout = DesktopLayoutSnapshot(
            openSessionIds = listOf("one"),
            focusedSessionId = "one",
            paneWeights = listOf(1f),
            detachedWindows = listOf(DesktopDetachedWindow("two", x = 40, y = 60, width = 700, height = 900)),
        )

        assertEquals(layout, decodeLayout(encodeLayout(layout)))
    }

    @Test
    fun `別ウィンドウの無い以前の保存値も読める`() {
        val restored = decodeLayout("""{"openSessionIds":["one"],"focusedSessionId":"one","paneWeights":[1.0]}""")

        assertEquals(listOf("one"), restored.openSessionIds)
        assertEquals(emptyList(), restored.detachedWindows)
    }

    @Test
    fun `ペインと重複する別ウィンドウと小さすぎるウィンドウを正す`() {
        val restored = DesktopLayoutSnapshot(
            openSessionIds = listOf("one"),
            detachedWindows = listOf(
                DesktopDetachedWindow("one"),
                DesktopDetachedWindow("two", width = 10, height = 10),
                DesktopDetachedWindow("two"),
                DesktopDetachedWindow(""),
            ),
        ).normalized()

        assertEquals(
            listOf(DesktopDetachedWindow("two", width = MIN_DETACHED_WIDTH, height = MIN_DETACHED_HEIGHT)),
            restored.detachedWindows,
        )
    }
}
