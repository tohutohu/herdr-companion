package com.tohutohu.herdrcompanion.ui.usage

import com.tohutohu.herdrcompanion.data.api.UsageDto

data class UsageCardUiState(
    val current: UsageDto,
    val nowMillis: Long,
    val expanded: Boolean,
    val refreshing: Boolean,
    val fetchedAtText: String?,
)

sealed interface UsageCardAction {
    data object Toggle : UsageCardAction
    data object Refresh : UsageCardAction
}
