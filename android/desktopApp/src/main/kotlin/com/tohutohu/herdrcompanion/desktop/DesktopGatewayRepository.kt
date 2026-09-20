package com.tohutohu.herdrcompanion.desktop

import com.tohutohu.herdrcompanion.data.api.GatewayApi
import com.tohutohu.herdrcompanion.data.api.InteractionResponseDto
import com.tohutohu.herdrcompanion.data.api.MessageDto
import com.tohutohu.herdrcompanion.data.api.SessionDto

/** Desktop's uncached repository; the Gateway/provider remains the source of truth. */
class DesktopGatewayRepository(private val api: GatewayApi) {
    suspend fun sessions(): List<SessionDto> = api.sessions()

    suspend fun session(id: String): SessionDto = api.session(id)

    suspend fun messages(id: String, after: String? = null): List<MessageDto> =
        api.messages(id, after).messages

    suspend fun messageSnapshot(id: String, after: String? = null) = api.messages(id, after)

    suspend fun send(id: String, text: String, uploads: List<String> = emptyList()) =
        api.sendMessage(id, text, uploads)

    suspend fun respond(id: String, response: InteractionResponseDto) = api.respond(id, response)
}
