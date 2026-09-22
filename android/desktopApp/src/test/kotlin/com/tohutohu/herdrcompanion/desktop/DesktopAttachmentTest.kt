package com.tohutohu.herdrcompanion.desktop

import java.nio.file.Files
import java.awt.image.BufferedImage
import javax.imageio.ImageIO
import kotlin.test.Test
import kotlin.test.assertEquals
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
}
