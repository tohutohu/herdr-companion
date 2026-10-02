package com.tohutohu.herdrcompanion.desktop

import java.io.File
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

/** GUI launches on Linux usually inherit the session PATH, unlike macOS apps. */
internal fun findOnPath(name: String, path: String? = System.getenv("PATH")): Path? =
    path.orEmpty().split(File.pathSeparatorChar)
        .filter { it.isNotBlank() }
        .map { Paths.get(it, name) }
        .firstOrNull { it.toFile().canExecute() }

/**
 * Terminal emulators that can run a script, most specific first. `$TERMINAL`
 * is the de facto user override; `x-terminal-emulator` is Debian's default.
 */
internal fun linuxTerminalCommand(
    script: String,
    environment: Map<String, String> = System.getenv(),
    find: (String) -> Path? = { findOnPath(it, environment["PATH"]) },
): List<String>? {
    environment["TERMINAL"]?.trim()?.takeIf { it.isNotEmpty() }?.let { terminal ->
        val executable = if (terminal.contains('/')) Paths.get(terminal).takeIf { it.toFile().canExecute() } else find(terminal)
        if (executable != null) return listOf(executable.toString(), "-e", script)
    }
    return listOf(
        "x-terminal-emulator" to listOf("-e"),
        "gnome-terminal" to listOf("--"),
        "konsole" to listOf("-e"),
        "xfce4-terminal" to listOf("-x"),
        "kitty" to emptyList(),
        "alacritty" to listOf("-e"),
        "xterm" to listOf("-e"),
    ).firstNotNullOfOrNull { (name, flags) ->
        find(name)?.let { listOf(it.toString()) + flags + script }
    }
}
