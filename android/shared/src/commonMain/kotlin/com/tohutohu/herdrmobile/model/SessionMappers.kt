package com.tohutohu.herdrmobile.model

import com.tohutohu.herdrmobile.data.api.SessionDto

/** Maps the gateway's transport model to the presentation model used by both JVM clients. */
fun SessionDto.toUiModel(): SessionUiModel = SessionUiModel(
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
    contextUsedTokens = context?.usedTokens,
    contextWindowTokens = context?.windowTokens,
    contextUsedPercent = context?.usedPercent,
    costUsd = cost?.usd,
    costEstimated = cost?.estimated,
    archived = archived,
    live = isLive,
)
