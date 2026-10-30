package com.tohutohu.herdrcompanion.desktop

import java.nio.file.Path
import java.nio.file.Paths

/** The operating systems the Desktop UI runs on; anything that is neither macOS nor Windows is treated as Linux. */
internal enum class DesktopOs {
    MAC,
    WINDOWS,
    LINUX,
    ;

    companion object {
        val current: DesktopOs = from(System.getProperty("os.name").orEmpty())

        fun from(osName: String): DesktopOs = when {
            osName.contains("mac", ignoreCase = true) || osName.contains("darwin", ignoreCase = true) -> MAC
            osName.contains("windows", ignoreCase = true) -> WINDOWS
            else -> LINUX
        }
    }
}

/** Where the per-user lock (and any future local state) lives, following each OS's convention. */
internal fun desktopStateDirectory(
    home: Path,
    os: DesktopOs = DesktopOs.current,
    environment: Map<String, String> = System.getenv(),
): Path = when (os) {
    DesktopOs.MAC -> home.resolve("Library/Application Support/Herdr Companion")
    DesktopOs.WINDOWS -> (environment["LOCALAPPDATA"]?.takeIf { it.isNotBlank() }?.let(Paths::get)
        ?: home.resolve("AppData").resolve("Local")).resolve("Herdr Companion")
    DesktopOs.LINUX -> (environment["XDG_STATE_HOME"]?.takeIf { it.isNotBlank() }?.let(Paths::get)
        ?: home.resolve(".local").resolve("state")).resolve("herdr-companion")
}

/** The command that hands a URL or file to the user's default application. */
internal fun systemOpenCommand(target: String, os: DesktopOs = DesktopOs.current): List<String> = when (os) {
    DesktopOs.MAC -> listOf("/usr/bin/open", target)
    DesktopOs.WINDOWS -> listOf("rundll32", "url.dll,FileProtocolHandler", target)
    DesktopOs.LINUX -> listOf("xdg-open", target)
}
