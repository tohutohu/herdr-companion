package com.tohutohu.herdrcompanion.ui.files

import okhttp3.HttpUrl.Companion.toHttpUrl
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class HtmlPageViewTest {
    private fun resolve(page: String, href: String) = page.toHttpUrl().resolve(href).toString()

    @Test
    fun `絶対パスのHTMLは同じパスに戻せるURLになる`() {
        val url = htmlPageUrl("/Users/me/work/report dir/index.html")
        assertEquals("/Users/me/work/report dir/index.html", gatewayPathForPageUrl(url))
    }

    @Test
    fun `ルート相対のパスは相対のまま戻る`() {
        assertEquals("docs/out/index.html", gatewayPathForPageUrl(htmlPageUrl("docs/out/index.html")))
    }

    @Test
    fun `相対リンクはページと同じフォルダのファイルを指す`() {
        val page = htmlPageUrl("/Users/me/work/out/index.html")
        assertEquals("/Users/me/work/out/style.css", gatewayPathForPageUrl(resolve(page, "style.css")))
        assertEquals("/Users/me/work/img/a.png", gatewayPathForPageUrl(resolve(page, "../img/a.png")))
    }

    @Test
    fun `日本語や記号を含むファイル名も往復できる`() {
        val path = "/Users/me/作業/レポート #1?.html"
        assertEquals(path, gatewayPathForPageUrl(htmlPageUrl(path)))
    }

    @Test
    fun `ワークスペース外のURLはファイルパスにしない`() {
        assertNull(gatewayPathForPageUrl("https://example.com/a/index.html"))
        assertNull(gatewayPathForPageUrl("https://$HTML_PAGE_HOST/"))
        assertNull(gatewayPathForPageUrl("https://$HTML_PAGE_HOST/x/index.html"))
        assertNull(gatewayPathForPageUrl("https://$HTML_PAGE_HOST/a/out/"))
    }

    @Test
    fun `テキストとして配信されたリソースに本来の種類を付ける`() {
        assertEquals("text/html", htmlResourceMimeType("/w/index.HTML", "text/plain; charset=utf-8"))
        assertEquals("text/css", htmlResourceMimeType("/w/style.css", "text/plain; charset=utf-8"))
        assertEquals("text/javascript", htmlResourceMimeType("/w/app.mjs", "text/plain; charset=utf-8"))
        assertEquals("image/svg+xml", htmlResourceMimeType("/w/logo.svg", "text/plain; charset=utf-8"))
    }
}
