package com.tohutohu.herdrcompanion.desktop

import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.Paths
import java.nio.file.attribute.PosixFilePermissions
import java.util.concurrent.TimeUnit
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

/**
 * Opens Terminal.app directly attached to the Herdr pane hosting a session.
 * Like the production Gateway, it talks to the default Herdr session.
 */
internal object DesktopHerdrTerminal {
    private const val PANE_LOOKUP_TIMEOUT_SECONDS = 5L

    /** Returns null once Terminal.app was asked to attach, otherwise a message for the user. */
    fun attach(paneId: String): String? {
        val herdr = findHerdr() ?: return "Could not find the herdr command on this Mac."
        val terminalId = runCatching { lookupTerminalId(herdr, paneId) }.getOrNull()
            ?: return "This session's Herdr pane is not open on this Mac."
        return runCatching {
            // A .command file runs in a new Terminal window without Apple Events permission.
            val script = Files.createTempFile("herdr-attach-", ".command")
            Files.writeString(script, attachScript(herdr.toString(), terminalId))
            Files.setPosixFilePermissions(script, PosixFilePermissions.fromString("rwx------"))
            ProcessBuilder("/usr/bin/open", "-a", "Terminal", script.toString()).start()
            null
        }.getOrElse { "Could not open Terminal." }
    }

    /** GUI apps do not inherit the login shell PATH, so look in the usual install locations. */
    private fun findHerdr(): Path? = listOf(
        Paths.get(System.getProperty("user.home"), ".local", "bin", "herdr"),
        Paths.get("/opt/homebrew/bin/herdr"),
        Paths.get("/usr/local/bin/herdr"),
    ).firstOrNull(Files::isExecutable)

    private fun lookupTerminalId(herdr: Path, paneId: String): String? {
        val process = ProcessBuilder(herdr.toString(), "pane", "get", paneId)
            .redirectError(ProcessBuilder.Redirect.DISCARD)
            .apply { environment().keys.removeIf { it.startsWith("HERDR_") } }
            .start()
        if (!process.waitFor(PANE_LOOKUP_TIMEOUT_SECONDS, TimeUnit.SECONDS)) {
            process.destroy()
            return null
        }
        return parseTerminalId(process.inputStream.bufferedReader().readText())
    }
}

/** Reads `result.pane.terminal_id` from `herdr pane get` output; errors have no result. */
internal fun parseTerminalId(output: String): String? = runCatching {
    Json.parseToJsonElement(output).jsonObject["result"]?.jsonObject
        ?.get("pane")?.jsonObject
        ?.get("terminal_id")?.jsonPrimitive?.contentOrNull
        ?.takeIf { it.isNotBlank() }
}.getOrNull()

/** Detaching (ctrl+b q) ends the script; it removes itself once Terminal has started it. */
internal fun attachScript(herdr: String, terminalId: String): String = """
    |#!/bin/sh
    |rm -f "${'$'}0"
    |unset HERDR_SOCKET_PATH HERDR_SESSION HERDR_PANE_ID
    |exec ${shellQuote(herdr)} terminal attach ${shellQuote(terminalId)}
    |
""".trimMargin()

private fun shellQuote(value: String): String = "'" + value.replace("'", "'\\''") + "'"
