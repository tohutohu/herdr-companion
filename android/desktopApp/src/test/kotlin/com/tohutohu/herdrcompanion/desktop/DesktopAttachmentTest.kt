package com.tohutohu.herdrcompanion.desktop

import coil3.ImageLoader
import coil3.PlatformContext
import coil3.request.ErrorResult
import coil3.request.ImageRequest
import coil3.request.SuccessResult
import java.nio.file.Files
import java.awt.image.BufferedImage
import java.io.ByteArrayOutputStream
import javax.imageio.ImageIO
import kotlinx.coroutines.runBlocking
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertTrue

class DesktopAttachmentTest {
    @Test
    fun `Finder path becomes the shared attachment identity`() {
        val file = Files.createTempFile("herdr-desktop", ".txt")
        try {
            val attachment = file.toDesktopAttachment()

            assertEquals(file.toAbsolutePath().normalize().toString(), attachment.id)
            assertEquals(file.fileName.toString(), attachment.name)
            assertTrue(attachment.mimeType.isNotBlank())
        } finally {
            Files.deleteIfExists(file)
        }
    }

    @Test
    fun `file URLs can be converted for drag and drop implementations`() {
        val file = Files.createTempFile("herdr-drop", ".md")
        try {
            assertEquals(file.toAbsolutePath().normalize(), fileUrlToPath(file.toUri().toString()))
        } finally {
            Files.deleteIfExists(file)
        }
    }

    @Test
    fun `clipboard images are normalized to PNG attachments`() {
        val source = BufferedImage(3, 2, BufferedImage.TYPE_INT_RGB)
        val target = Files.createTempFile("herdr-clipboard-test", ".png")
        try {
            assertTrue(writeImageAsPng(source, target))
            val saved = ImageIO.read(target.toFile())
            assertEquals(3, saved.width)
            assertEquals(2, saved.height)
        } finally {
            Files.deleteIfExists(target)
        }
    }

    @Test
    fun `クリップボードのPNGはピクセル数を変えずにそのまま保存する`() {
        val source = BufferedImage(6, 4, BufferedImage.TYPE_INT_ARGB)
        val bytes = ByteArrayOutputStream().also { ImageIO.write(source, "png", it) }.toByteArray()
        val target = Files.createTempFile("herdr-clipboard-png", ".png")
        try {
            assertTrue(writePngBytes(bytes, target))
            assertContentEquals(bytes, Files.readAllBytes(target))
        } finally {
            Files.deleteIfExists(target)
        }
    }

    @Test
    fun `PNGでないバイト列はPNGとして保存しない`() {
        val target = Files.createTempFile("herdr-clipboard-png", ".png")
        try {
            assertFalse(writePngBytes("not a png".toByteArray(), target))
        } finally {
            Files.deleteIfExists(target)
        }
    }

    @Test
    fun `クリップボードのTIFFは元の解像度のPNGに変換する`() {
        val source = BufferedImage(6, 4, BufferedImage.TYPE_INT_RGB)
        val tiff = ByteArrayOutputStream().also { ImageIO.write(source, "tiff", it) }.toByteArray()
        val target = Files.createTempFile("herdr-clipboard-tiff", ".png")
        try {
            assertTrue(writeImageBytesAsPng(tiff, target))
            val saved = ImageIO.read(target.toFile())
            assertEquals(6, saved.width)
            assertEquals(4, saved.height)
        } finally {
            Files.deleteIfExists(target)
        }
    }

    @Test
    fun `添付画像のプレビューをCoilで読み込める`(): Unit = runBlocking {
        val file = Files.createTempFile("herdr-preview", ".png")
        try {
            ImageIO.write(BufferedImage(4, 3, BufferedImage.TYPE_INT_ARGB), "png", file.toFile())
            val context = PlatformContext.INSTANCE
            val result = ImageLoader(context).execute(
                ImageRequest.Builder(context).data(file.toDesktopAttachment().previewModel).build(),
            )
            assertIs<SuccessResult>(result, (result as? ErrorResult)?.throwable?.message)
        } finally {
            Files.deleteIfExists(file)
        }
    }
}
