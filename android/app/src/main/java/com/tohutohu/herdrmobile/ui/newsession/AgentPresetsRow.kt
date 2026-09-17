package com.tohutohu.herdrmobile.ui.newsession

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Tune
import androidx.compose.material3.FilledTonalIconButton
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrmobile.data.AgentPreset

/**
 * Favorite agent / model / effort combinations as a chip row, with the way
 * out to the full pickers pinned next to it: the favorites scroll, that
 * button does not. A [current] combination that is not saved leads the row
 * with a chip of its own, so the row always shows what will be started.
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

    Row(
        modifier = modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        LazyRow(
            modifier = Modifier.weight(1f),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            // Room for the chips to scroll out from under the button.
            contentPadding = PaddingValues(end = 4.dp),
        ) {
            if (presets.none { it.key == current.key }) {
                // Not a favorite: tapping it reopens the pickers it came from.
                item(key = "current") {
                    FilterChip(
                        selected = true,
                        onClick = onCustomize,
                        label = { Text(presetLabel(current)) },
                        leadingIcon = { Selected() },
                    )
                }
            }
            items(presets, key = { "preset:${it.key}" }) { preset ->
                val selected = preset.key == current.key
                FilterChip(
                    selected = selected,
                    onClick = { onSelect(preset) },
                    label = { Text(presetLabel(preset)) },
                    leadingIcon = if (selected) ({ Selected() }) else null,
                )
            }
        }
        FilledTonalIconButton(onClick = onCustomize) {
            Icon(Icons.Default.Tune, contentDescription = "Other agent, model or effort")
        }
    }
}
