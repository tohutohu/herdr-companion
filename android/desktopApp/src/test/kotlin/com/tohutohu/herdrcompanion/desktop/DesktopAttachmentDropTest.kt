package com.tohutohu.herdrcompanion.desktop

import java.awt.datatransfer.DataFlavor
import java.awt.datatransfer.Transferable
import java.awt.datatransfer.UnsupportedFlavorException
import java.awt.dnd.InvalidDnDOperationException
import java.io.File
import java.nio.file.Files
import kotlin.io.path.deleteIfExists
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class DesktopAttachmentDropTest {
    private fun fileList(read: () -> Any?) = object : Transferable {
        override fun getTransferDataFlavors() = arrayOf(DataFlavor.javaFileListFlavor)
        override fun isDataFlavorSupported(flavor: DataFlavor) = flavor == DataFlavor.javaFileListFlavor
        override fun getTransferData(flavor: DataFlavor): Any? = read()
    }

    @Test
    fun `ドロップされたファイルだけを添付候補にしてフォルダは除く`() {
        val file = Files.createTempFile("herdr-drop", ".txt")
        val folder = Files.createTempDirectory("herdr-drop")
        try {
            val dropped = fileList { listOf(file.toFile(), folder.toFile()) }.droppedFiles()

            assertEquals(listOf(file), dropped)
        } finally {
            file.deleteIfExists()
            folder.deleteIfExists()
        }
    }

    @Test
    fun `もう存在しないファイルは添付候補にしない`() {
        val gone = File(System.getProperty("java.io.tmpdir"), "herdr-drop-gone-${System.nanoTime()}")

        assertTrue(fileList { listOf(gone) }.droppedFiles().isEmpty())
    }

    @Test
    fun `ファイルを含まないドラッグは受け付けない`() {
        val text = object : Transferable {
            override fun getTransferDataFlavors() = arrayOf(DataFlavor.stringFlavor)
            override fun isDataFlavorSupported(flavor: DataFlavor) = flavor == DataFlavor.stringFlavor
            override fun getTransferData(flavor: DataFlavor): Any = throw UnsupportedFlavorException(flavor)
        }

        assertTrue(text.droppedFiles().isEmpty())
    }

    @Test
    fun `ドラッグの内容を読めなくても例外を外に出さない`() {
        val unreadable = fileList { throw InvalidDnDOperationException("No drop current") }

        assertTrue(unreadable.droppedFiles().isEmpty())
    }
}
