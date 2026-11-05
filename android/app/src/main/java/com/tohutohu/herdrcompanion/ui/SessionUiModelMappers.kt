package com.tohutohu.herdrcompanion.ui

import com.tohutohu.herdrcompanion.data.SessionStart
import com.tohutohu.herdrcompanion.data.api.GatewayApi
import com.tohutohu.herdrcompanion.data.backgroundTasksSerializer
import com.tohutohu.herdrcompanion.data.api.Status
import com.tohutohu.herdrcompanion.data.db.SessionEntity
import com.tohutohu.herdrcompanion.model.PendingStartUiState
import com.tohutohu.herdrcompanion.model.SessionUiModel
import com.tohutohu.herdrcompanion.model.toUiModel as toTaskUiModel

/** Converts the Android Room row into the platform-neutral presentation model. */
fun SessionEntity.toUiModel() = SessionUiModel(
    id = id,
    provider = provider,
    providerName = providerName,
    project = project,
    title = title,
    cwd = cwd,
    status = status,
    lastMessage = lastMessage,
    canSend = canSend,
    model = model,
    effort = effort,
    mode = mode,
    contextUsedTokens = contextUsedTokens,
    contextWindowTokens = contextWindowTokens,
    contextUsedPercent = contextUsedPercent,
    costUsd = costUsd,
    costEstimated = costEstimated,
    archived = archived,
    live = status != Status.OFFLINE,
    unread = unread,
    translation = translation,
    pendingModel = pendingModel,
    pendingEffort = pendingEffort,
    settingsApply = settingsApply,
    error = error,
    background = backgroundJson
        ?.let { runCatching { GatewayApi.json.decodeFromString(backgroundTasksSerializer, it) }.getOrNull() }
        .orEmpty()
        .map { it.toTaskUiModel() },
)

/** Converts Android-owned launch tracking into the common list state. */
fun SessionStart.toUiState() = PendingStartUiState(
    id = id,
    cwd = request.cwd,
    prompt = request.prompt,
    label = label,
    busy = busy,
    sessionId = sessionId,
)
