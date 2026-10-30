package com.tohutohu.herdrcompanion.model

import com.tohutohu.herdrcompanion.data.api.ContextUsageDto
import com.tohutohu.herdrcompanion.data.api.CostDto
import com.tohutohu.herdrcompanion.data.api.SessionDto
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class SessionMappersTest {
    @Test
    fun `セッションDTOを一覧表示モデルへ変換する`() {
        val model = SessionDto(
            id = "claude:1",
            provider = "claude",
            providerName = "Claude Code",
            project = "herdr",
            status = "working",
            updatedAt = "2026-09-20T00:00:00Z",
            context = ContextUsageDto(12, 100, 12),
            cost = CostDto(0.04, true),
        ).toUiModel()

        assertEquals("Claude Code", model.providerName)
        assertEquals(12, model.contextUsedPercent)
        assertEquals(0.04, model.costUsd)
        assertTrue(model.live)
    }

    @Test
    fun `Herdrの未読状態を一覧表示モデルへ引き継ぐ`() {
        val model = SessionDto(
            id = "claude:1",
            provider = "claude",
            status = "completed",
            updatedAt = "2026-09-20T00:00:00Z",
            unread = true,
        ).toUiModel()

        assertTrue(model.unread)
    }
    @Test
    fun `直接実行の実行先と保留中の設定と失敗理由を表示モデルへ引き継ぐ`() {
        val model = SessionDto(
            id = "codex:1",
            provider = "codex",
            status = "running",
            updatedAt = "2026-10-30T00:00:00Z",
            backend = "direct",
            pendingModel = "gpt-6-mini",
            pendingEffort = "low",
            settingsApply = "next_turn",
            error = "runtime exited",
        ).toUiModel()

        assertEquals("direct", model.backend)
        assertEquals("gpt-6-mini", model.pendingModel)
        assertEquals("low", model.pendingEffort)
        assertEquals("next_turn", model.settingsApply)
        assertEquals("runtime exited", model.error)
        assertTrue(model.direct)
    }

    @Test
    fun `Herdrで動くセッションと停止中のセッションは直接実行として扱わない`() {
        val herdr = SessionDto(id = "claude:1", provider = "claude", status = "running", updatedAt = "", backend = "herdr", paneId = "w1:p1")
        assertFalse(herdr.toUiModel().direct)
        val offline = SessionDto(id = "claude:1", provider = "claude", status = "offline", updatedAt = "", backend = "direct")
        assertFalse(offline.toUiModel().direct)
    }
}
