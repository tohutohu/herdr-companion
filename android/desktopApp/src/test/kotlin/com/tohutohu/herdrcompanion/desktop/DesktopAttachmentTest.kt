package com.tohutohu.herdrcompanion.desktop

import java.nio.file.Files
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
}
