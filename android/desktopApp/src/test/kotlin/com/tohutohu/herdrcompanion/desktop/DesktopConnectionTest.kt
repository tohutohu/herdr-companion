package com.tohutohu.herdrcompanion.desktop

import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

class DesktopConnectionTest {
    @Test
    fun `macOS manager config supplies token and local default port`() {
        val connection = DesktopConnectionConfig.parseConfig(
            """{"authToken":"secret"}""",
            "http://127.0.0.1:8766",
        )

        assertEquals("http://127.0.0.1:8766", connection.baseUrl)
        assertEquals("secret", connection.token)
        assertNull(connection.configIssue)
    }

    @Test
    fun `explicit gateway URL in config wins over default`() {
        val connection = DesktopConnectionConfig.parseConfig(
            """{"authToken":"secret","gatewayUrl":"http://localhost:9999"}""",
            "http://127.0.0.1:8766",
        )

        assertEquals("http://localhost:9999", connection.baseUrl)
    }

    @Test
    fun `a configured non-default listen address is honored`() {
        val connection = DesktopConnectionConfig.parseConfig(
            """{"authToken":"secret","listen":"100.99.15.34:8765"}""",
            "http://127.0.0.1:8766",
        )

        assertEquals("http://100.99.15.34:8765", connection.baseUrl)
    }

    @Test
    fun `設定画面で保存した接続先とトークンはマネージャーの設定より優先される`() = withHome { home ->
        writeManagerConfig(home, """{"authToken":"manager"}""")
        DesktopManualConnectionStore.save(home, DesktopManualConnection("http://100.64.0.1:8766", "typed"))

        val connection = DesktopConnectionConfig.load(home, environment = emptyMap(), properties = emptyMap())

        assertEquals("http://100.64.0.1:8766", connection.baseUrl)
        assertEquals("typed", connection.token)
        assertTrue(connection.manual)
        assertNull(connection.configIssue)
    }

    @Test
    fun `手動接続を消すとマネージャーの設定に戻る`() = withHome { home ->
        writeManagerConfig(home, """{"authToken":"manager"}""")
        val source = DesktopConnectionSource(home, environment = emptyMap(), properties = emptyMap())
        source.saveManual(DesktopManualConnection("http://100.64.0.1:8766", "typed"))

        val restored = source.clearManual()

        assertEquals("manager", restored.token)
        assertFalse(restored.manual)
        assertFalse(Files.exists(DesktopManualConnectionStore.path(home)))
    }

    @Test
    fun `設定ファイルがなければ設定画面での入力を案内する`() = withHome { home ->
        val connection = DesktopConnectionConfig.load(home, environment = emptyMap(), properties = emptyMap())

        assertFalse(connection.settings.isConfigured)
        assertTrue(connection.configIssue.orEmpty().contains("Settings"))
    }

    @Test
    fun `壊れた手動接続ファイルは無視してマネージャーの設定を使う`() = withHome { home ->
        writeManagerConfig(home, """{"authToken":"manager"}""")
        val manual = DesktopManualConnectionStore.path(home)
        Files.createDirectories(manual.parent)
        Files.writeString(manual, "not json")

        val connection = DesktopConnectionConfig.load(home, environment = emptyMap(), properties = emptyMap())

        assertEquals("manager", connection.token)
        assertFalse(connection.manual)
    }

    @Test
    fun `入力された接続先は前後の空白と末尾のスラッシュを除いて保存する`() {
        val connection = validateManualConnection(" http://100.64.0.1:8766/ ", " token ").getOrThrow()

        assertEquals("http://100.64.0.1:8766", connection.gatewayUrl)
        assertEquals("token", connection.token)
    }

    @Test
    fun `HTTPでもHTTPSでもない接続先やパス付きの接続先は受け付けない`() {
        listOf("100.64.0.1:8766", "ftp://host", "http://", "http://host/api", "http://user@host", "http://host?x=1")
            .forEach { url -> assertTrue(validateManualConnection(url, "token").isFailure, url) }
        assertNotNull(validateManualConnection("https://gateway.example.com", "token").getOrNull())
    }

    @Test
    fun `空や空白を含むトークンは受け付けない`() {
        assertTrue(validateManualConnection("http://host:8766", " ").isFailure)
        assertTrue(validateManualConnection("http://host:8766", "a b").isFailure)
    }

    private fun writeManagerConfig(home: Path, json: String) {
        val path = home.resolve(".config/herdr-mobile/desktop/config.json")
        Files.createDirectories(path.parent)
        Files.writeString(path, json)
    }

    private fun withHome(block: (Path) -> Unit) {
        val home = Files.createTempDirectory("herdr-desktop-connection")
        try {
            block(home)
        } finally {
            home.toFile().deleteRecursively()
        }
    }
}
