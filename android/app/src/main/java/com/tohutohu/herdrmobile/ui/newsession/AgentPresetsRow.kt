package com.tohutohu.herdrmobile.ui.newsession

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Tune
import androidx.compose.material3.AssistChip
import androidx.compose.material3.AssistChipDefaults
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrmobile.data.AgentPreset

/**
 * Favorite agent / model / effort combinations as a chip row, followed by the
 * way out to the full pickers. A [current] combination that is not saved gets
 * a chip of its own, so the row always shows what will be started.
 */
@Composable
fun AgentPresetsRow(
    presets: List<AgentPreset>,
    current: AgentPreset,
    onSelect: (AgentPreset) -> Unit,
    onCustomize: () -> Unit,
    modifier: Modifier = Modifier,
) {
    @Composable
    fun Selected() = Icon(Icons.Default.Check, contentDescription = "Selected", Modifier.size(FilterChipDefaults.IconSize))

    LazyRow(
        modifier = modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        items(presets, key = { "preset:${it.key}" }) { preset ->
            val selected = preset.key == current.key
            FilterChip(
                selected = selected,
                onClick = { onSelect(preset) },
                label = { Text(presetLabel(preset)) },
                leadingIcon = if (selected) ({ Selected() }) else null,
            )
        }
        if (presets.none { it.key == current.key }) {
            // Not a favorite: tapping it reopens the pickers it was made in.
            item(key = "current") {
                FilterChip(
                    selected = true,
                    onClick = onCustomize,
                    label = { Text(presetLabel(current)) },
                    leadingIcon = { Selected() },
                )
            }
        }
        item(key = "customize") {
            AssistChip(
                onClick = onCustomize,
                label = { Text("Other…") },
                leadingIcon = {
                    Icon(Icons.Default.Tune, contentDescription = null, Modifier.size(AssistChipDefaults.IconSize))
                },
            )
        }
    }
}
