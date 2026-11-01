package com.tohutohu.herdrcompanion.ui.detail

import kotlin.test.Test
import kotlin.test.assertEquals

class ComposerDraftsTest {
    @Test
    fun セッションごとに別の下書きを持ち空にすると忘れる() {
        val drafts = ComposerDrafts()
        drafts["claude:a"] = "途中まで"
        drafts["codex:b"] = "別の指示"
        assertEquals("途中まで", drafts["claude:a"])
        assertEquals("別の指示", drafts["codex:b"])
        drafts["claude:a"] = ""
        assertEquals("", drafts["claude:a"])
        assertEquals(mapOf("codex:b" to "別の指示"), ComposerDrafts.decode(ComposerDrafts.encode(mapOf("codex:b" to "別の指示"))))
    }

    @Test
    fun 上限を超えると最も前に編集した下書きから捨てる() {
        val drafts = ComposerDrafts()
        repeat(ComposerDrafts.MAX_DRAFTS) { drafts["s$it"] = "text $it" }
        drafts["s0"] = "edited again"
        drafts["new"] = "newest"
        assertEquals("edited again", drafts["s0"])
        assertEquals("", drafts["s1"])
        assertEquals("newest", drafts["new"])
    }

    @Test
    fun 読めない保存内容は下書きなしとして扱う() {
        assertEquals(emptyMap(), ComposerDrafts.decode(null))
        assertEquals(emptyMap(), ComposerDrafts.decode("{broken"))
        assertEquals("", ComposerDrafts(mapOf("claude:a" to ""))["claude:a"])
    }

    @Test
    fun 外から足した文は下書きの後ろに空行を挟んで付く() {
        val drafts = ComposerDrafts()
        drafts.append("claude:a", "コメント1")
        assertEquals("コメント1", drafts["claude:a"])
        drafts["claude:a"] = "途中まで\n"
        drafts.append("claude:a", "コメント2")
        assertEquals("途中まで\n\nコメント2", drafts["claude:a"])
        drafts.append("claude:a", "  ")
        assertEquals("途中まで\n\nコメント2", drafts["claude:a"])
    }
}
