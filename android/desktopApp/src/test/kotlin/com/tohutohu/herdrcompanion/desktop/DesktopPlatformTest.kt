package com.tohutohu.herdrcompanion.desktop

import java.nio.file.Path
import java.nio.file.Paths
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class DesktopPlatformTest {
    private val home: Path = Paths.get("/home/user")

    @Test
    fun `os_nameからmacOSとWindowsとLinuxを見分ける`() {
        assertEquals(DesktopOs.MAC, DesktopOs.from("Mac OS X"))
        assertEquals(DesktopOs.WINDOWS, DesktopOs.from("Windows 11"))
        assertEquals(DesktopOs.LINUX, DesktopOs.from("Linux"))
        assertEquals(DesktopOs.LINUX, DesktopOs.from("FreeBSD"))
    }

    @Test
    fun `macOSのロック置き場は従来のApplication Supportのまま`() {
        assertEquals(
            home.resolve("Library/Application Support/Herdr Companion"),
            desktopStateDirectory(home, DesktopOs.MAC, emptyMap()),
        )
    }

    @Test
    fun `Linuxのロック置き場はXDG_STATE_HOMEに従い未設定なら既定の場所を使う`() {
        assertEquals(
            Paths.get("/xdg/state/herdr-companion"),
            desktopStateDirectory(home, DesktopOs.LINUX, mapOf("XDG_STATE_HOME" to "/xdg/state")),
        )
        assertEquals(
            home.resolve(".local/state/herdr-companion"),
            desktopStateDirectory(home, DesktopOs.LINUX, emptyMap()),
        )
    }

    @Test
    fun `LinuxではURLやファイルをxdg-openで開く`() {
        assertEquals(listOf("xdg-open", "https://example.com"), systemOpenCommand("https://example.com", DesktopOs.LINUX))
        assertEquals(listOf("/usr/bin/open", "/tmp/a"), systemOpenCommand("/tmp/a", DesktopOs.MAC))
    }

    @Test
    fun `Linuxの通知はnotify-sendでタイトルをオプションとして解釈させない`() {
        val event = DesktopNotificationEvent(id = "1", title = "-project", body = "Agent finished")

        assertEquals(
            listOf("notify-send", "--app-name=Herdr Companion", "--", "-project", "Agent finished"),
            notificationCommand(event, DesktopOs.LINUX),
        )
        assertNull(notificationCommand(event, DesktopOs.WINDOWS))
    }

    @Test
    fun `端末はTERMINALの指定を優先しなければ見つかったものを順に使う`() {
        val installed = setOf("gnome-terminal", "xterm", "foot")
        val find = { name: String -> if (name in installed) Paths.get("/usr/bin", name) else null }

        assertEquals(
            listOf("/usr/bin/foot", "-e", "/tmp/s"),
            linuxTerminalCommand("/tmp/s", mapOf("TERMINAL" to "foot"), find),
        )
        assertEquals(
            listOf("/usr/bin/gnome-terminal", "--", "/tmp/s"),
            linuxTerminalCommand("/tmp/s", emptyMap(), find),
        )
        assertNull(linuxTerminalCommand("/tmp/s", emptyMap()) { null })
    }
}
