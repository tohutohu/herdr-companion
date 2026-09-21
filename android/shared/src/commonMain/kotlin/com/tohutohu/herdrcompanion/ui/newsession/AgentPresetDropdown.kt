package com.tohutohu.herdrcompanion.ui.newsession

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExposedDropdownMenuAnchorType
import androidx.compose.material3.ExposedDropdownMenuBox
import androidx.compose.material3.ExposedDropdownMenuDefaults
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import com.tohutohu.herdrcompanion.data.AgentPreset

/** A compact, named preset selector for the new-session form. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AgentPresetDropdown(
    presets: List<AgentPreset>,
    current: AgentPreset,
    onSelect: (AgentPreset) -> Unit,
    onCustomize: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var expanded by remember { mutableStateOf(false) }
    val selected = presets.firstOrNull { it.key == current.key }
    val value = selected?.let(::presetTitle) ?: "Custom selection"

    ExposedDropdownMenuBox(
        expanded = expanded,
        onExpandedChange = { expanded = it },
        modifier = modifier,
    ) {
        OutlinedTextField(
            value = value,
            onValueChange = {},
            readOnly = true,
            singleLine = true,
            label = { Text("Session preset") },
            supportingText = if (selected != null && selected.name.isNotBlank()) {
                { Text(presetDetails(selected)) }
            } else if (selected == null && current.key != "") {
                { Text(presetDetails(current)) }
            } else {
                null
            },
            trailingIcon = { ExposedDropdownMenuDefaults.TrailingIcon(expanded = expanded) },
            modifier = Modifier
                .fillMaxWidth()
                .menuAnchor(ExposedDropdownMenuAnchorType.PrimaryNotEditable),
        )
        ExposedDropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
            if (presets.isEmpty()) {
                DropdownMenuItem(
                    text = {
                        Column {
                            Text("No saved presets")
                            Text("Choose manually to start", style = androidx.compose.material3.MaterialTheme.typography.bodySmall)
                        }
                    },
                    onClick = {
                        expanded = false
                        onCustomize()
                    },
                )
            } else {
                presets.forEach { preset ->
                    DropdownMenuItem(
                        text = {
                            Column {
                                Text(presetTitle(preset))
                                Text(
                                    presetDetails(preset),
                                    style = androidx.compose.material3.MaterialTheme.typography.bodySmall,
                                )
                            }
                        },
                        onClick = {
                            expanded = false
                            onSelect(preset)
                        },
                    )
                }
            }
            DropdownMenuItem(
                text = { Text("Choose manually…") },
                onClick = {
                    expanded = false
                    onCustomize()
                },
            )
        }
    }
}
