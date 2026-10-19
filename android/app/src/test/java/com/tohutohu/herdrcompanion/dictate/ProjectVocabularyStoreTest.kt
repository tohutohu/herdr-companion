package com.tohutohu.herdrcompanion.dictate

import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

class ProjectVocabularyStoreTest {
    @get:Rule val folder = TemporaryFolder()

    private fun store() = ProjectVocabularyStore(folder.root.resolve("dictate/project-vocabulary.json"))

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
    fun `大文字小文字だけ違う語は登録済みとして扱いrevisionを進めない`() {
        val store = store()
        assertEquals(AddResult.OK, store.addVocabulary("p", "Kotlin"))
        assertEquals(AddResult.ALREADY_EXISTS, store.addVocabulary("p", "kotlin"))
        assertEquals(1L, store["p"].revision)
    }

    @Test
    fun `長い置換ルールは短いルールより前に並ぶ`() {
        val store = store()
        store.addReplacement("p", "ミドル", "Middle")
        store.addReplacement("p", "ミドルウェア", "Middleware")
        store.addReplacement("p", "ア", "A")
        assertEquals(listOf("ミドルウェア", "ミドル", "ア"), store["p"].replacements.map { it.from })
    }

    @Test
    fun `同じfromの置換ルールは置換後の文字列を上書きする`() {
        val store = store()
        store.addReplacement("p", "はーだー", "herder")
        assertEquals(AddResult.OK, store.addReplacement("p", "はーだー", "Herdr"))
        assertEquals(AddResult.ALREADY_EXISTS, store.addReplacement("p", "はーだー", "Herdr"))
        assertEquals(listOf("Herdr"), store["p"].replacements.map { it.to })
    }

    @Test
    fun `削除するとrevisionが進み空になったプロジェクトも世代を保つ`() {
        val store = store()
        store.addVocabulary("p", "Kotlin")
        store.removeVocabulary("p", "Kotlin")
        assertEquals(emptyList<String>(), store()["p"].vocabulary)
        assertEquals(2L, store()["p"].revision)
    }

    @Test
    fun `壊れたファイルは空として読み込む`() {
        folder.root.resolve("dictate").mkdirs()
        folder.root.resolve("dictate/project-vocabulary.json").writeText("{")
        assertEquals(emptyMap<String, ProjectVocabulary>(), store().projects.value)
    }
}
