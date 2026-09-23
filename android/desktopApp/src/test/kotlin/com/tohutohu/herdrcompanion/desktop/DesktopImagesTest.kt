package com.tohutohu.herdrcompanion.desktop

import coil3.PlatformContext
import coil3.request.ErrorResult
import coil3.request.ImageRequest
import coil3.request.SuccessResult
import com.sun.net.httpserver.HttpServer
import com.tohutohu.herdrcompanion.data.Settings
import java.awt.image.BufferedImage
import java.io.ByteArrayOutputStream
import java.net.InetSocketAddress
import javax.imageio.ImageIO
import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertIs

class DesktopImagesTest {
    private lateinit var server: HttpServer
    private lateinit var baseUrl: String

    @BeforeTest
    fun start() {
        val png = ByteArrayOutputStream()
            .also { ImageIO.write(BufferedImage(4, 3, BufferedImage.TYPE_INT_ARGB), "png", it) }
            .toByteArray()
        server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0).apply {
            createContext("/v1/sessions/s/messages/m/images/0") { exchange ->
                if (exchange.requestHeaders.getFirst("Authorization") == "Bearer secret") {
                    exchange.responseHeaders.add("Content-Type", "image/png")
                    exchange.sendResponseHeaders(200, png.size.toLong())
                    exchange.responseBody.use { it.write(png) }
                } else {
                    exchange.sendResponseHeaders(401, -1)
                    exchange.close()
                }
            }
            start()
        }
        baseUrl = "http://127.0.0.1:${server.address.port}"
    }

    @AfterTest
    fun stop() = server.stop(0)

    private fun load(settings: Settings) = runBlocking {
        val context = PlatformContext.INSTANCE
        desktopImageLoader(context, OkHttpClient()) { settings }
            .execute(ImageRequest.Builder(context).data("$baseUrl/v1/sessions/s/messages/m/images/0").build())
    }

    @Test
    fun `Gatewayのメッセージ画像をトークン付きで読み込める`() {
        val result = load(Settings(gatewayUrl = baseUrl, token = "secret"))
        assertIs<SuccessResult>(result, (result as? ErrorResult)?.throwable?.message)
    }

    @Test
    fun `別のオリジンにはトークンを送らない`() {
        val result = load(Settings(gatewayUrl = "http://127.0.0.1:1", token = "secret"))
        assertIs<ErrorResult>(result)
    }
}
