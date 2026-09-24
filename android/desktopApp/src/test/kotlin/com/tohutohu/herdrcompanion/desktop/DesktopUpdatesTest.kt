package com.tohutohu.herdrcompanion.desktop

import java.nio.file.Paths
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull

class DesktopUpdatesTest {
    private val sha = "432c6b4feed0eb9bde88bcc1c36418d04d72ec087bf190a51a9c01fed2528bd9"

    private fun release(
        tag: String = "v1.2.0",
        digest: String? = "sha256:$sha",
        withChecksumFile: Boolean = true,
        prerelease: Boolean = false,
    ) = GitHubRelease(
        tagName = tag,
        htmlUrl = "https://github.com/tohutohu/herdr-companion/releases/tag/$tag",
        prerelease = prerelease,
        assets = buildList {
            val version = tag.removePrefix("v")
            add(GitHubAsset("Herdr-Companion-Gateway-$version.dmg", "https://example.test/gateway.dmg", 10, "sha256:${"0".repeat(64)}"))
            add(GitHubAsset("Herdr-Companion-$version.dmg", "https://example.test/ui.dmg", 62, digest))
            if (withChecksumFile) add(GitHubAsset("Herdr-Companion-$version.dmg.sha256", "https://example.test/ui.dmg.sha256"))
        },
    )

    @Test
    fun `バージョンは数値として比較し足りない桁は0とみなす`() {
        assertEquals(1, compareVersions("1.10.0", "1.9.9"))
        assertEquals(0, compareVersions("v1.2", "1.2.0"))
        assertEquals(-1, compareVersions("1.1.0", "2"))
        assertNull(compareVersions("1.2.0-beta", "1.1.0"))
        assertNull(compareVersions("1.2.3.4", "1.1.0"))
    }

    @Test
    fun `新しいリリースではUIのDMGとGitHubのダイジェストを選ぶ`() {
        val update = assertNotNull(selectUpdate(release(), "1.1.0", "Herdr-Companion"))

        assertEquals("1.2.0", update.version)
        assertEquals("Herdr-Companion-1.2.0.dmg", update.dmgName)
        assertEquals("https://example.test/ui.dmg", update.dmgUrl)
        assertEquals(sha, update.sha256)
    }

    @Test
    fun `同じか古いリリースとプレリリースは更新として扱わない`() {
        assertNull(selectUpdate(release(tag = "v1.1.0"), "1.1.0", "Herdr-Companion"))
        assertNull(selectUpdate(release(tag = "v1.0.0"), "1.1.0", "Herdr-Companion"))
        assertNull(selectUpdate(release(prerelease = true), "1.1.0", "Herdr-Companion"))
    }

    @Test
    fun `ダイジェストがなければチェックサムファイルで検証する`() {
        val update = assertNotNull(selectUpdate(release(digest = null), "1.1.0", "Herdr-Companion"))

        assertNull(update.sha256)
        assertEquals("https://example.test/ui.dmg.sha256", update.sha256Url)
    }

    @Test
    fun `検証手段のないDMGは更新として扱わない`() {
        assertNull(selectUpdate(release(digest = null, withChecksumFile = false), "1.1.0", "Herdr-Companion"))
    }

    @Test
    fun `チェックサムファイルは対象ファイルの行だけを読む`() {
        val text = "${"a".repeat(64)}  Herdr-Companion-Gateway-1.2.0.dmg\n$sha  Herdr-Companion-1.2.0.dmg\n"

        assertEquals(sha, parseSha256File(text, "Herdr-Companion-1.2.0.dmg"))
        assertEquals(sha, parseSha256File("${sha.uppercase()} *Herdr-Companion-1.2.0.dmg", "Herdr-Companion-1.2.0.dmg"))
        assertNull(parseSha256File("$sha  other.dmg", "Herdr-Companion-1.2.0.dmg"))
        assertNull(parseSha256File("abc  Herdr-Companion-1.2.0.dmg", "Herdr-Companion-1.2.0.dmg"))
    }

    @Test
    fun `ランチャーのパスからアプリバンドルを求める`() {
        assertEquals(
            Paths.get("/Applications/Herdr Companion.app"),
            appBundleFor("/Applications/Herdr Companion.app/Contents/MacOS/Herdr Companion"),
        )
        assertNull(appBundleFor("/Users/me/android/desktopApp/build/classes"))
        assertNull(appBundleFor(null))
    }

    @Test
    fun `DMGや隔離された場所から起動したアプリは置き換えない`() {
        assertNotNull(installBlocker(Paths.get("/Volumes/Herdr Companion/Herdr Companion.app"), parentWritable = false))
        assertNotNull(
            installBlocker(Paths.get("/private/var/folders/x/AppTranslocation/ABC/d/Herdr Companion.app"), parentWritable = true),
        )
        assertNotNull(installBlocker(Paths.get("/Applications/Herdr Companion.app"), parentWritable = false))
        assertNull(installBlocker(Paths.get("/Applications/Herdr Companion.app"), parentWritable = true))
    }
}
