package com.tohutohu.herdrcompanion.ui

import androidx.navigation3.runtime.NavBackStack
import org.junit.Assert.assertEquals
import org.junit.Test

class NavigatorTest {
    @Test
    fun 別セッションを開いて戻ると元のセッションに戻れる() {
        val stack = NavBackStack<Route>(SessionsRoute, DetailRoute("claude:one"))
        val navigator = Navigator(stack)
        navigator.openLinkedSession("claude:two")
        assertEquals(DetailRoute("claude:two"), stack.last())
        navigator.back()
        assertEquals(DetailRoute("claude:one"), stack.last())
    }

    @Test
    fun すでに開いたセッションへのリンクは既存の画面を再利用する() {
        val original = DetailRoute("claude:one", focusLatest = true)
        val stack = NavBackStack<Route>(SessionsRoute, original, DetailRoute("claude:two"))
        val navigator = Navigator(stack)
        navigator.openLinkedSession("claude:one")
        assertEquals(listOf(SessionsRoute, original), stack.toList())
        navigator.openLinkedSession("claude:one")
        assertEquals(listOf(SessionsRoute, original), stack.toList())
    }

    @Test
    fun 常駐タスクの通知を開くと開いていたタスクと編集画面を置き換える() {
        val stack = NavBackStack<Route>(ResidentHomeRoute, ResidentTaskRoute("task_a"), ResidentFormRoute("task_a"))
        Navigator(stack).openResidentFromNotification(ResidentTaskRoute("task_b", fromNotification = true))
        assertEquals(listOf(ResidentHomeRoute, ResidentTaskRoute("task_b", fromNotification = true)), stack.toList())
    }

    @Test
    fun 作成したタスクは一覧の上に開き作成画面を残さない() {
        val stack = NavBackStack<Route>(ResidentHomeRoute, ResidentFormRoute())
        Navigator(stack).replaceAbove(ResidentHomeRoute, ResidentTaskRoute("task_new"))
        assertEquals(listOf(ResidentHomeRoute, ResidentTaskRoute("task_new")), stack.toList())
    }
}
