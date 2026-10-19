package com.tohutohu.herdrcompanion.dictate

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

class ProjectVocabularyStoreTest {
    @get:Rule val folder = TemporaryFolder()

    private fun store() = ProjectVocabularyStore(folder.root.resolve("dictate/project-vocabulary.json"))

    private val ProjectVocabulary.rules get() = replacements.map { it.from to it.to }

    @Test
    fun `語彙はプロジェクトごとに分けて保存され再読み込みしても残る`() {
        store().apply {
            addVocabulary("herdr-android-client", "Herdr")
            addVocabulary("onnoti-tracker", "OnNoti")
        }
        val reloaded = store()
        assertEquals(listOf("Herdr"), reloaded["herdr-android-client"].vocabulary)
        assertEquals(listOf("OnNoti"), reloaded["onnoti-tracker"].vocabulary)
        assertEquals(emptyList<String>(), reloaded["other"].vocabulary)
    }

    @Test
    fun `読み付きの語は語彙と読みから語への置換ルールを一度に保存する`() {
        val store = store()
        assertTrue(store.addVocabulary("p", "OnNoti Tracker", listOf("オンノティトラッカー", "オンノティ トラッカー")))
        assertEquals(listOf("OnNoti Tracker"), store()["p"].vocabulary)
        assertEquals(
            listOf("オンノティ トラッカー" to "OnNoti Tracker", "オンノティトラッカー" to "OnNoti Tracker"),
            store()["p"].rules,
        )
        assertEquals(1L, store()["p"].revision)
    }

    @Test
    fun `語も読みもすべて登録済みなら何も変えずrevisionを進めない`() {
        val store = store()
        store.addVocabulary("p", "Kotlin", listOf("ことりん"))
        assertFalse(store.addVocabulary("p", "Kotlin", listOf("ことりん")))
        assertEquals(1L, store["p"].revision)
    }

    @Test
    fun `登録済みの語に新しい読みだけ足せる`() {
        val store = store()
        store.addVocabulary("p", "Kotlin")
        assertTrue(store.addVocabulary("p", "Kotlin", listOf("ことりん")))
        assertEquals(listOf("Kotlin"), store["p"].vocabulary)
        assertEquals(listOf("ことりん" to "Kotlin"), store["p"].rules)
    }

    @Test
    fun `読みから作るルールは長いものが短いものより前に並ぶ`() {
        val store = store()
        store.addVocabulary("p", "Middle", listOf("ミドル"))
        store.addVocabulary("p", "Middleware", listOf("ミドルウェア"))
        store.addVocabulary("p", "A", listOf("ア"))
        assertEquals(listOf("ミドルウェア", "ミドル", "ア"), store["p"].replacements.map { it.from })
    }

    @Test
    fun `別の語に使われていた読みは新しい語に付け替える`() {
        val store = store()
        store.addVocabulary("p", "herder", listOf("はーだー"))
        store.addVocabulary("p", "Herdr", listOf("はーだー"))
        assertEquals(listOf("はーだー" to "Herdr"), store["p"].rules)
    }

    @Test
    fun `語彙を削除しても置換ルールは残る`() {
        val store = store()
        store.addVocabulary("p", "Kotlin", listOf("ことりん"))
        assertTrue(store.deleteVocabulary("p", "Kotlin"))
        assertFalse(store.deleteVocabulary("p", "Kotlin"))
        assertEquals(emptyList<String>(), store()["p"].vocabulary)
        assertEquals(listOf("ことりん" to "Kotlin"), store()["p"].rules)
        assertEquals(2L, store()["p"].revision)
    }

    @Test
    fun `置換ルールはidがあればidでなければfromとtoで削除する`() {
        val store = store()
        store.addVocabulary("p", "Kotlin", listOf("ことりん", "コトリン"))
        val id = store["p"].replacements.first { it.from == "ことりん" }.id
        assertTrue(store.deleteReplacement("p", id, "違う", "値"))
        assertTrue(store.deleteReplacement("p", null, "コトリン", "Kotlin"))
        assertFalse(store.deleteReplacement("p", null, "コトリン", "Kotlin"))
        assertEquals(emptyList<Pair<String, String>>(), store["p"].rules)
    }

    @Test
    fun `壊れたファイルは空として読み込む`() {
        folder.root.resolve("dictate").mkdirs()
        folder.root.resolve("dictate/project-vocabulary.json").writeText("{")
        assertEquals(emptyMap<String, ProjectVocabulary>(), store().projects.value)
    }
}
