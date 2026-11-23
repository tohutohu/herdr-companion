package com.tohutohu.herdrcompanion.ui.usage

import androidx.compose.foundation.layout.width
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.toAwtImage
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.getBoundsInRoot
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.data.api.UsageDto
import com.tohutohu.herdrcompanion.data.api.UsageProviderDto
import com.tohutohu.herdrcompanion.data.api.UsageWindowDto
import org.junit.Rule
import org.junit.Test
import java.io.File
import java.time.Instant
import javax.imageio.ImageIO
import kotlin.test.assertTrue

class UsageCardTest {
    @get:Rule val rule = createComposeRule()

    private val now = Instant.parse("2026-11-23T09:10:00Z").toEpochMilli()

    private fun window(label: String, used: Int, minutes: Int, reset: String, scope: String? = null) =
        UsageWindowDto(key = label + scope, label = label, scope = scope, usedPercent = used, windowMinutes = minutes, resetsAt = reset)

    private val usage = UsageDto(
        providers = listOf(
            UsageProviderDto(
                provider = "claude",
                displayName = "Claude",
                windows = listOf(
                    window("5h", 27, 300, "2026-11-23T13:00:00Z"),
                    window("7d", 27, 10080, "2026-11-28T01:00:00Z"),
                    window("7d", 37, 10080, "2026-11-28T01:00:00Z", scope = "Fable only"),
                ),
            ),
            UsageProviderDto(
                provider = "codex",
                displayName = "Codex",
                plan = "plus",
                windows = listOf(
                    window("5h", 0, 300, "2026-11-23T14:06:00Z"),
                    window("7d", 5, 10080, "2026-11-24T13:05:00Z"),
                ),
            ),
            UsageProviderDto(
                provider = "opencode-go",
                displayName = "OpenCode Go",
                windows = listOf(
                    window("5h", 0, 300, "2026-11-23T14:04:00Z"),
                    window("7d", 1, 10080, "2026-11-29T23:59:00Z"),
                    window("30d", 61, 43200, "2026-12-07T13:17:00Z"),
                ),
            ),
        ),
        fetchedAt = "2026-11-23T09:06:00Z",
    )

    @Test
    fun 展開しても3プロバイダーの使用量がスマホ画面の4分の1ほどに収まる() {
        var expanded by mutableStateOf(false)
        rule.setContent {
            MaterialTheme(colorScheme = darkColorScheme()) {
                Surface(Modifier.width(360.dp).testTag("card")) {
                    UsageCard(
                        state = UsageCardUiState(usage, now, expanded, refreshing = false, fetchedAtText = "updated 4m ago"),
                        onAction = { if (it == UsageCardAction.Toggle) expanded = !expanded },
                    )
                }
            }
        }
        System.getenv("USAGE_CARD_SHOT")?.let { path ->
            ImageIO.write(rule.onNodeWithTag("card").captureToImage().toAwtImage(), "png", File(path.replace(".png", "-collapsed.png")))
        }
        rule.onNodeWithContentDescription("Expand limits").performClick()
        rule.onNodeWithText("Fable", substring = true).assertExists()
        rule.onNodeWithText("updated 4m ago").assertExists()
        val height = rule.onNodeWithTag("card").getBoundsInRoot().let { it.bottom - it.top }
        System.getenv("USAGE_CARD_SHOT")?.let { path ->
            ImageIO.write(rule.onNodeWithTag("card").captureToImage().toAwtImage(), "png", File(path))
        }
        // 1280×2856 at 480 dpi is about 950dp tall; the old card took most of it.
        assertTrue(height < 260.dp, "expanded card is $height tall")
    }
}
