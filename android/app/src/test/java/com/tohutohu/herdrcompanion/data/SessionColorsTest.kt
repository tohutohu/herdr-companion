package com.tohutohu.herdrcompanion.data

import android.app.Application
import com.tohutohu.herdrcompanion.ui.sessions.SessionColor
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(application = Application::class, sdk = [35])
class SessionColorsTest {
    @Test
    fun 色を一括保存して別インスタンスで読み戻し一件だけ解除できる() = runBlocking {
        val context = RuntimeEnvironment.getApplication()
        val store = SessionColorsStore(context)
        store.set(listOf("codex:one", "claude:two"), SessionColor.Purple)
        val restored = SessionColorsStore(context)
        assertEquals(
            mapOf("codex:one" to SessionColor.Purple, "claude:two" to SessionColor.Purple),
            restored.colors.first(),
        )
        restored.set(listOf("codex:one"), null)
        assertEquals(mapOf("claude:two" to SessionColor.Purple), store.colors.first())
    }
}
