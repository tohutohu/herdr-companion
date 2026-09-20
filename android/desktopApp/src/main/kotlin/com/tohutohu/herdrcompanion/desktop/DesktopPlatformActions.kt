package com.tohutohu.herdrcompanion.desktop

import java.awt.Desktop
import java.awt.Toolkit
import java.awt.datatransfer.StringSelection
import java.net.URI
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.Paths

/** Small Desktop-only bridge for operations that should not enter commonMain. */
object DesktopPlatformActions {
    private const val GATEWAY_MANAGER_BUNDLE_ID = "com.tohutohu.herdrmobile.mac"

    fun copyText(text: String): Boolean = runCatching {
        Toolkit.getDefaultToolkit().systemClipboard.setContents(StringSelection(text), null)
        true
    }.getOrDefault(false)

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

    fun openTerminal(directory: String?): Boolean {
        val dir = directory?.let { resolvePath(it, null) }?.takeIf { Files.isDirectory(it) } ?: return false
        return runCatching {
            ProcessBuilder("/usr/bin/open", "-a", "Terminal", dir.toString()).start()
            true
        }.getOrDefault(false)
    }

    /** Ask the separately installed SwiftUI manager to start its owned Gateway. */
    fun requestGatewayStart(): Boolean = runCatching {
        ProcessBuilder(
            "/usr/bin/open",
            "-b",
            GATEWAY_MANAGER_BUNDLE_ID,
            "herdr-mobile://gateway/start",
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
