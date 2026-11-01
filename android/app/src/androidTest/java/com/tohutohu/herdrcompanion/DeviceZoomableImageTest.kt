package com.tohutohu.herdrcompanion

import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Paint
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.doubleClick
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.pinch
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.tohutohu.herdrcompanion.test.DeviceTestActivity
import com.tohutohu.herdrcompanion.ui.files.ZoomableImage
import java.io.ByteArrayOutputStream
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/** Real multi-touch input on the image viewer's zoomable picture. */
@RunWith(AndroidJUnit4::class)
class DeviceZoomableImageTest {
    @get:Rule val rule = createEmptyComposeRule()
    private var scenario: ActivityScenario<DeviceTestActivity>? = null

    @After fun closeActivity() {
        scenario?.close()
        DeviceTestActivity.content = {}
    }

    @Test fun ピンチした場所を中心に拡大しダブルタップで戻して再び拡大できる() {
        val png = quadrants()
        DeviceTestActivity.content = {
            ZoomableImage(png, contentDescription = null, modifier = Modifier.fillMaxSize().testTag("image"))
        }
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        check(context.packageName.endsWith(".devicetest")) { "Device tests require the isolated APK" }
        scenario = ActivityScenario.launch(DeviceTestActivity::class.java)
        val image = rule.onNodeWithTag("image")
        // The square picture is fitted to the width and centred vertically.
        fun pixel(at: Offset): Int = image.captureToImage().asAndroidBitmap().getPixel(at.x.toInt(), at.y.toInt())
        var redCentre = Offset.Zero
        var greenCentre = Offset.Zero
        rule.waitUntil(10_000) {
            val shot = image.captureToImage().asAndroidBitmap()
            val side = shot.width.toFloat()
            val top = (shot.height - side) / 2
            redCentre = Offset(side / 4, top + side / 4)
            greenCentre = Offset(side * 3 / 4, top + side / 4)
            shot.getPixel(redCentre.x.toInt(), redCentre.y.toInt()) == RED
        }
        assertEquals(GREEN, pixel(greenCentre))

        image.performTouchInput {
            val spread = Offset(30f, 0f)
            pinch(redCentre - spread, redCentre - spread * 4f, redCentre + spread, redCentre + spread * 4f, durationMillis = 400)
        }
        rule.waitForIdle()
        // The red quadrant grew around the fingers and now covers where green was.
        assertEquals(RED, pixel(redCentre))
        assertEquals(RED, pixel(greenCentre))

        image.performTouchInput { doubleClick(redCentre) }
        rule.waitForIdle()
        assertEquals(GREEN, pixel(greenCentre))

        image.performTouchInput { doubleClick(redCentre) }
        rule.waitForIdle()
        assertEquals(RED, pixel(redCentre))
        assertEquals(RED, pixel(greenCentre))
    }

    /** A 200px PNG: red, green / blue, yellow. */
    private fun quadrants(): ByteArray {
        val bitmap = Bitmap.createBitmap(200, 200, Bitmap.Config.ARGB_8888)
        val canvas = Canvas(bitmap)
        val paint = Paint()
        listOf(RED to (0 to 0), GREEN to (100 to 0), BLUE to (0 to 100), YELLOW to (100 to 100)).forEach { (color, at) ->
            paint.color = color
            canvas.drawRect(at.first.toFloat(), at.second.toFloat(), at.first + 100f, at.second + 100f, paint)
        }
        return ByteArrayOutputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it); it.toByteArray() }
    }

    private companion object {
        const val RED = 0xFFFF0000.toInt()
        const val GREEN = 0xFF00FF00.toInt()
        const val BLUE = 0xFF0000FF.toInt()
        const val YELLOW = 0xFFFFFF00.toInt()
    }
}
