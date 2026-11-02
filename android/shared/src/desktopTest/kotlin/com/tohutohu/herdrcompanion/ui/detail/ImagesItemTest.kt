package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.width
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.graphics.toPixelMap
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onAllNodesWithContentDescription
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.data.Message
import com.tohutohu.herdrcompanion.data.api.BlockDto
import java.awt.image.BufferedImage
import java.io.File
import javax.imageio.ImageIO
import kotlin.test.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

class ImagesItemTest {
    @get:Rule val rule = createComposeRule()

    @get:Rule val dir = TemporaryFolder()

    private fun png(name: String, color: java.awt.Color): String {
        val image = BufferedImage(400, 300, BufferedImage.TYPE_INT_RGB)
        image.createGraphics().apply {
            this.color = color
            fillRect(0, 0, 400, 300)
            dispose()
        }
        val file = dir.newFile("$name.png")
        ImageIO.write(image, "png", file)
        return file.absolutePath
    }

    @Test
    fun 続けて返った画像は同じ高さで横に並びタップした画像を開く() {
        val urls = listOf(png("red", java.awt.Color.RED), png("green", java.awt.Color.GREEN), png("blue", java.awt.Color.BLUE))
        val row = ImagesRow("images:t1#0", 0, urls.mapIndexed { i, url -> Message("t$i", "tool", 0, listOf(BlockDto(type = "image", url = url))) })
        var opened: String? = null
        rule.setContent {
            Box(Modifier.width(360.dp)) { ImagesItem(row, resolveUrl = { it }, onOpenImage = { opened = it }) }
        }
        val px = { dp: Int -> (dp * rule.density.density).toInt() }
        // 160 dp high, so the 4:3 images are 213 dp wide with 8 dp between them.
        val middle = px(2 + 80)
        rule.waitUntil(10_000) { colorAt(capture(), px(12 + 100), middle) == Color.Red.toArgb() }
        val image = capture()
        save(image, "tool-images")
        assertEquals(px(160 + 4), image.height)
        assertEquals(Color.Red.toArgb(), colorAt(image, px(12 + 100), middle))
        assertEquals(Color.Green.toArgb(), colorAt(image, px(12 + 213 + 8 + 50), middle))

        rule.onAllNodesWithContentDescription("Image")[1].performClick()
        assertEquals(urls[1], opened)
    }

    private fun capture(): ImageBitmap = rule.onRoot().captureToImage()

    private fun colorAt(image: ImageBitmap, x: Int, y: Int): Int = image.toPixelMap()[x, y].toArgb()

    private fun save(image: ImageBitmap, name: String) {
        val pixels = image.toPixelMap()
        val output = BufferedImage(image.width, image.height, BufferedImage.TYPE_INT_ARGB)
        for (y in 0 until image.height) for (x in 0 until image.width) output.setRGB(x, y, pixels[x, y].toArgb())
        val file = File("build/reports/audit-ui/$name.png")
        file.parentFile.mkdirs()
        ImageIO.write(output, "png", file)
    }
}
