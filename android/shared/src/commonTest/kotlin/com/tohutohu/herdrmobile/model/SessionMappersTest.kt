package com.tohutohu.herdrmobile.model

import com.tohutohu.herdrmobile.data.api.ContextUsageDto
import com.tohutohu.herdrmobile.data.api.CostDto
import com.tohutohu.herdrmobile.data.api.SessionDto
import kotlin.test.Test
import kotlin.test.assertEquals
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
}
