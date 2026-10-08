package com.tohutohu.herdrcompanion.ui.newsession

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material3.Text
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.SemanticsNodeInteraction
import androidx.compose.ui.test.getBoundsInRoot
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.swipeLeft
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import kotlin.math.abs
import kotlin.test.assertEquals
import kotlin.test.assertTrue
import org.junit.Rule
import org.junit.Test

class ReorderableChipRowTest {
    @get:Rule
    val rule = createComposeRule()

    private var items by mutableStateOf(listOf("A", "B", "C", "D"))
    private val moves = mutableListOf<List<String>>()

    private fun setRow() {
        rule.setContent {
            ReorderableChipRow(
                rowItems = items,
                itemKey = { it },
                onMove = {
                    moves += it
                    items = it
                },
                modifier = Modifier.width(400.dp),
                contentPadding = PaddingValues(horizontal = PADDING),
                horizontalArrangement = Arrangement.spacedBy(SPACING),
            ) { item, reorderModifier ->
                Box(reorderModifier.testTag(item).size(CHIP, 32.dp)) { Text(item) }
            }
        }
    }

    private fun chip(tag: String): SemanticsNodeInteraction = rule.onNodeWithTag(tag)

    private fun left(tag: String): Dp = chip(tag).getBoundsInRoot().left

    private fun slot(index: Int): Dp = PADDING + (CHIP + SPACING) * index

    private fun assertNear(expected: Dp, actual: Dp, message: String) {
        assertTrue(abs((expected - actual).value) < 1f, "$message: expected $expected, actual $actual")
    }

    /** Long-presses [tag] until the drag has started. */
    private fun lift(tag: String) {
        chip(tag).performTouchInput { down(center) }
        rule.mainClock.advanceTimeBy(1_000)
        // The first move after the long press only starts the drag.
        chip(tag).performTouchInput { moveBy(Offset(1f, 0f)) }
        rule.waitForIdle()
    }

    /** Long-presses [tag] and drags it by [distance] in small steps, laying out between them. */
    private fun drag(tag: String, distance: Dp, release: Boolean = true) {
        lift(tag)
        val stepPx = with(rule.density) { (distance / STEPS).toPx() }
        repeat(STEPS) {
            chip(tag).performTouchInput { moveBy(Offset(stepPx, 0f)) }
            rule.waitForIdle()
        }
        if (release) {
            chip(tag).performTouchInput { up() }
            rule.waitForIdle()
        }
    }

    @Test
    fun ドラッグ中のチップは入れ替わる瞬間も指から離れない() {
        setRow()
        lift("A")
        rule.mainClock.autoAdvance = false

        // Past B's center, so the chips swap on the way.
        repeat(12) {
            val before = left("A")
            chip("A").performTouchInput { moveBy(Offset(8f, 0f)) }
            rule.mainClock.advanceTimeByFrame()
            val step = with(rule.density) { 8f.toDp() }
            assertNear(before + step, left("A"), "フレーム $it で A は指と同じだけ動く")
        }
    }

    @Test
    fun ドロップしたチップは新しい枠まで滑らかに動く() {
        setRow()
        drag("A", (CHIP + SPACING) * 1.5f, release = false)
        val lifted = left("A")
        rule.mainClock.autoAdvance = false

        chip("A").performTouchInput { up() }
        rule.mainClock.advanceTimeByFrame()
        assertTrue(left("A") > slot(1) + 10.dp, "離した直後は枠へ飛ばない: ${left("A")}")
        assertTrue(left("A") <= lifted, "枠へ向かって戻り始める: ${left("A")}")

        rule.mainClock.advanceTimeBy(1_000)
        assertNear(slot(1), left("A"), "A は 2 番目の枠に収まる")
        assertEquals(listOf(listOf("B", "A", "C", "D")), moves)
    }

    @Test
    fun スクロールした行で先頭のチップを動かしても隣のチップが消えない() {
        items = listOf("A", "B", "C", "D", "E", "F", "G", "H")
        setRow()
        chip("B").performTouchInput { swipeLeft(startX = right - 2f, endX = right - 42f, durationMillis = 200) }
        rule.waitForIdle()
        val scrolled = chip("C").getBoundsInRoot().left - slot(2)
        assertTrue(scrolled < 0.dp && left("A") <= 0.dp, "A が一部見えている所までスクロールしている: $scrolled")

        drag("A", CHIP + SPACING + 10.dp, release = false)

        // B takes A's slot; the row itself stays where it was.
        assertNear(slot(2) + scrolled, left("C"), "C は動かない")
        assertTrue(chip("B").getBoundsInRoot().right > 0.dp, "B は画面に残る")
    }

    @Test
    fun 途中のチップを先頭へ動かすと先頭の枠に収まる() {
        setRow()

        drag("C", -(CHIP + SPACING) * 2.5f)

        assertEquals(listOf(listOf("C", "A", "B", "D")), moves)
        assertNear(slot(0), left("C"), "C は先頭の枠に収まる")
        assertNear(slot(1), left("A"), "A は 1 つ右へずれる")
    }

    private companion object {
        val CHIP = 60.dp
        val SPACING = 8.dp
        val PADDING = 16.dp
        const val STEPS = 10
    }
}
