package com.tohutohu.herdrmobile.ui

import com.tohutohu.herdrmobile.data.SessionStart
import com.tohutohu.herdrmobile.data.api.Status
import com.tohutohu.herdrmobile.data.db.SessionEntity
import com.tohutohu.herdrmobile.model.PendingStartUiState
import com.tohutohu.herdrmobile.model.SessionUiModel

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
    paneId = paneId,
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
