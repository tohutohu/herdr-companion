package com.tohutohu.herdrcompanion.data.api

import kotlinx.serialization.Serializable

@Serializable
data class AgentUpdatesResponse(val agents: List<AgentUpdateDto> = emptyList())

@Serializable
data class AgentUpdateDto(
    val provider: String,
    val version: String = "",
    val state: String = "idle",
    val output: String = "",
    val error: String = "",
)
