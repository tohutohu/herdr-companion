package com.tohutohu.herdrmobile.ui.newsession

import com.tohutohu.herdrmobile.data.api.EffortOptionDto
import com.tohutohu.herdrmobile.data.api.ModelOptionDto
import com.tohutohu.herdrmobile.data.api.ModelsResponse
import org.junit.Assert.assertEquals
import org.junit.Test

class EffortOptionsTest {
    private val catalog = ModelsResponse(
        models = listOf(
            ModelOptionDto(
                id = "gpt-6-astra",
                name = "GPT-6 Astra",
                efforts = listOf(EffortOptionDto("medium", "Medium", default = true), EffortOptionDto("xhigh", "Extra high")),
            ),
            ModelOptionDto(id = "gpt-6-mini", name = "Mini"),
        ),
        efforts = listOf(EffortOptionDto("low", "Low")),
    )

    @Test
    fun `モデルごとのエフォートを出す`() {
        assertEquals(listOf("medium", "xhigh"), effortsFor(catalog, "gpt-6-astra").map { it.id })
    }

    @Test
    fun `モデル未指定やエフォートを持たないモデルではカタログのエフォートを出す`() {
        assertEquals(listOf("low"), effortsFor(catalog, "").map { it.id })
        assertEquals(listOf("low"), effortsFor(catalog, "gpt-6-mini").map { it.id })
    }

    @Test
    fun `エフォートが無いカタログでは空になる`() {
        assertEquals(emptyList<String>(), effortsFor(ModelsResponse(), "").map { it.id })
    }
}
