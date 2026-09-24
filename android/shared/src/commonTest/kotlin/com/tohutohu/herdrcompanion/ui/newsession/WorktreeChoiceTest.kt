package com.tohutohu.herdrcompanion.ui.newsession

import com.tohutohu.herdrcompanion.data.AgentPreset
import com.tohutohu.herdrcompanion.data.DirectoryShortcuts
import com.tohutohu.herdrcompanion.data.api.DirListingDto
import com.tohutohu.herdrcompanion.data.api.ModelsResponse
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class WorktreeChoiceTest {
    private fun state(git: Boolean?, worktree: Boolean) = NewSessionUiState(
        provider = "claude",
        path = "/workspace/app",
        listing = git?.let { DirListingDto(path = "/workspace/app", git = it) },
        loading = false,
        error = null,
        prompt = "",
        starting = false,
        checking = false,
        pendingStart = null,
        showMkdir = false,
        showPicker = false,
        model = "",
        effort = "",
        mode = "",
        catalog = ModelsResponse(),
        modelsLoading = false,
        modelsError = null,
        shortcuts = DirectoryShortcuts(),
        savedPresets = emptyList(),
        currentPreset = AgentPreset("claude"),
        favorite = false,
        worktree = worktree,
    )

    @Test
    fun `worktreeはgitリポジトリのフォルダでだけ選べて開始ボタンにも表す`() {
        assertTrue(state(git = true, worktree = true).startsInWorktree)
        assertEquals("Start Claude Code in app (worktree)", startLabel(state(git = true, worktree = true)))
        assertEquals("Start Claude Code in app", startLabel(state(git = true, worktree = false)))
    }

    @Test
    fun `リポジトリでないフォルダや一覧の読み込み前は選択が残っていてもworktreeで始めない`() {
        for (git in listOf(false, null)) {
            val s = state(git = git, worktree = true)
            assertFalse(s.worktreeAvailable)
            assertFalse(s.startsInWorktree)
            assertEquals("Start Claude Code in app", startLabel(s))
        }
    }
}
