package com.tohutohu.herdrcompanion.dictate

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class DictateProjectTest {
    @Test
    fun `セッションのプロジェクト名があればそれを使う`() {
        assertEquals("herdr", dictateProject("herdr", "/Users/me/workspace/other"))
    }

    @Test
    fun `新規セッションではディレクトリのフォルダ名をプロジェクトにする`() {
        assertEquals("herdr-android-client", dictateProject(null, "/Users/me/workspace/herdr-android-client/"))
        assertEquals("herdr-android-client", dictateProject("", "/Users/me/workspace/herdr-android-client"))
    }

    @Test
    fun `ディレクトリ未選択ならプロジェクトは無い`() {
        assertNull(dictateProject(null, ""))
        assertNull(dictateProject(null, "/"))
        assertNull(dictateProject(null, null))
    }
}
