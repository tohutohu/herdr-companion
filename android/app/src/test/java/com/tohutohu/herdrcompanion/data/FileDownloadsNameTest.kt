package com.tohutohu.herdrcompanion.data

import java.util.TimeZone
import org.junit.Assert.assertEquals
import org.junit.Test

class FileDownloadsNameTest {
    @Test
    fun `同名で保存できないときは拡張子の前に日時を入れた名前にする`() {
        val tz = TimeZone.getDefault()
        TimeZone.setDefault(TimeZone.getTimeZone("Asia/Tokyo"))
        try {
            val t = 1_793_342_332_000L // 2026-10-30 15:38:52 JST
            assertEquals("app-release-20261030-153852.apk", FileDownloads.timestampedName("app-release.apk", t))
            assertEquals("archive.tar-20261030-153852.gz", FileDownloads.timestampedName("archive.tar.gz", t))
            assertEquals("README-20261030-153852", FileDownloads.timestampedName("README", t))
            assertEquals(".env-20261030-153852", FileDownloads.timestampedName(".env", t))
        } finally {
            TimeZone.setDefault(tz)
        }
    }
}
