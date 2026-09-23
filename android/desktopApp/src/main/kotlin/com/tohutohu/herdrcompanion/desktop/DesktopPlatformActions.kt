package com.tohutohu.herdrcompanion.desktop

import java.awt.Desktop
import java.awt.Image
import java.awt.Toolkit
import java.awt.datatransfer.Clipboard
import java.awt.datatransfer.DataFlavor
import java.awt.datatransfer.StringSelection
import java.awt.datatransfer.SystemFlavorMap
import java.awt.image.BufferedImage
import java.io.ByteArrayInputStream
import java.io.InputStream
import java.net.URI
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.Paths
import javax.imageio.ImageIO

/** Small Desktop-only bridge for operations that should not enter commonMain. */
object DesktopPlatformActions {
    private const val GATEWAY_MANAGER_BUNDLE_ID = "com.tohutohu.herdrcompanion.gateway"

    fun copyText(text: String): Boolean = runCatching {
        Toolkit.getDefaultToolkit().systemClipboard.setContents(StringSelection(text), null)
        true
    }.getOrDefault(false)

    /**
     * Copies the current clipboard image to a PNG file owned by the caller.
     * Returning null also covers clipboards that only contain text, allowing
     * the native text-field paste handling to continue.
     */
    fun pasteImage(): Path? = runCatching {
        val clipboard = Toolkit.getDefaultToolkit().systemClipboard
        val path = Files.createTempFile("herdr-clipboard-", ".png")
        val written = clipboardBytes(clipboard, pngFlavor)?.let { writePngBytes(it, path) } == true ||
            clipboardBytes(clipboard, tiffFlavor)?.let { writeImageBytesAsPng(it, path) } == true ||
            (clipboard.getDataOrNull(DataFlavor.imageFlavor) as? Image)?.let { writeImageAsPng(it, path) } == true
        if (!written) {
            Files.deleteIfExists(path)
            null
        } else {
            path
        }
    }.getOrNull()

    /*
     * AWT's imageFlavor sizes a macOS pasteboard image in points, which halves
     * a Retina screenshot. These flavors read the pasteboard's own PNG or TIFF
     * bytes, which keep every pixel.
     */
    private val pngFlavor = rawPasteboardFlavor("PNG")
    private val tiffFlavor = rawPasteboardFlavor("TIFF")

    private fun rawPasteboardFlavor(native: String): DataFlavor =
        DataFlavor("image/x-herdr-${native.lowercase()}; class=java.io.InputStream").also { flavor ->
            (SystemFlavorMap.getDefaultFlavorMap() as? SystemFlavorMap)?.apply {
                addUnencodedNativeForFlavor(flavor, native)
                addFlavorForUnencodedNative(native, flavor)
            }
        }

    private fun clipboardBytes(clipboard: Clipboard, flavor: DataFlavor): ByteArray? =
        (clipboard.getDataOrNull(flavor) as? InputStream)?.use(InputStream::readBytes)

    private fun Clipboard.getDataOrNull(flavor: DataFlavor): Any? = runCatching {
        if (isDataFlavorAvailable(flavor)) getData(flavor) else null
    }.getOrNull()

    fun openUrl(url: String): Boolean = runCatching {
        val uri = URI(url)
        require(uri.scheme == "http" || uri.scheme == "https")
        if (Desktop.isDesktopSupported() && Desktop.getDesktop().isSupported(Desktop.Action.BROWSE)) {
            Desktop.getDesktop().browse(uri)
        } else {
            ProcessBuilder("/usr/bin/open", url).start()
        }
        true
    }.getOrDefault(false)

    fun openPath(path: String, baseDirectory: String? = null): Boolean {
        val resolved = resolvePath(path, baseDirectory) ?: return false
        return runCatching {
            if (Desktop.isDesktopSupported() && Desktop.getDesktop().isSupported(Desktop.Action.OPEN)) {
                Desktop.getDesktop().open(resolved.toFile())
            } else {
                ProcessBuilder("/usr/bin/open", resolved.toString()).start()
            }
            true
        }.getOrDefault(false)
    }

    fun revealInFinder(path: String, baseDirectory: String? = null): Boolean {
        val resolved = resolvePath(path, baseDirectory) ?: return false
        return runCatching {
            ProcessBuilder("/usr/bin/open", "-R", resolved.toString()).start()
            true
        }.getOrDefault(false)
    }

    /** Ask the separately installed SwiftUI manager to start its owned Gateway. */
    fun requestGatewayStart(): Boolean = runCatching {
        ProcessBuilder(
            "/usr/bin/open",
            "-b",
            GATEWAY_MANAGER_BUNDLE_ID,
            "herdr-companion://gateway/start",
        ).start()
        true
    }.getOrDefault(false)

    private fun resolvePath(path: String, baseDirectory: String?): Path? {
        val candidate = Paths.get(path)
        if (candidate.isAbsolute) return candidate
        val base = baseDirectory?.takeIf { it.isNotBlank() }?.let(Paths::get) ?: return candidate
        return base.resolve(candidate).normalize()
    }
}

private val PNG_SIGNATURE = byteArrayOf(0x89.toByte(), 'P'.code.toByte(), 'N'.code.toByte(), 'G'.code.toByte())

/** Saves PNG bytes as they are, so nothing is re-encoded or rescaled. */
internal fun writePngBytes(bytes: ByteArray, path: Path): Boolean = runCatching {
    if (bytes.size < PNG_SIGNATURE.size || !bytes.copyOfRange(0, PNG_SIGNATURE.size).contentEquals(PNG_SIGNATURE)) {
        return@runCatching false
    }
    Files.write(path, bytes)
    true
}.getOrDefault(false)

/** Decodes an image file format ImageIO knows, such as TIFF, and saves it as PNG. */
internal fun writeImageBytesAsPng(bytes: ByteArray, path: Path): Boolean = runCatching {
    val image = ImageIO.read(ByteArrayInputStream(bytes)) ?: return@runCatching false
    writeImageAsPng(image, path)
}.getOrDefault(false)

/** Converts any AWT image representation into a format the attachment API accepts. */
internal fun writeImageAsPng(image: Image, path: Path): Boolean = runCatching {
    val width = image.getWidth(null)
    val height = image.getHeight(null)
    if (width <= 0 || height <= 0) return@runCatching false

    val buffered = if (image is BufferedImage && image.type == BufferedImage.TYPE_INT_ARGB) {
        image
    } else {
        BufferedImage(width, height, BufferedImage.TYPE_INT_ARGB).also { destination ->
            val graphics = destination.createGraphics()
            try {
                graphics.drawImage(image, 0, 0, null)
            } finally {
                graphics.dispose()
            }
        }
    }
    ImageIO.write(buffered, "png", path.toFile())
}.getOrDefault(false)
