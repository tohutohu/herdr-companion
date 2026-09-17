package com.tohutohu.herdrmobile.ui.newsession

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Star
import androidx.compose.material.icons.filled.StarBorder
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExposedDropdownMenuAnchorType
import androidx.compose.material3.ExposedDropdownMenuBox
import androidx.compose.material3.ExposedDropdownMenuDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrmobile.data.api.CatalogOption
import com.tohutohu.herdrmobile.data.api.ModelsResponse

/**
 * The full agent / model / effort pickers, for combinations that are not
 * among the favorites. The current one can be saved as a favorite from here,
 * which is also the only way to drop one again.
 */
@Composable
fun AgentPickerDialog(
    provider: String,
    model: String,
    effort: String,
    catalog: ModelsResponse,
    modelsError: String?,
    favorite: Boolean,
    onProvider: (String) -> Unit,
    onModel: (String) -> Unit,
    onEffort: (String) -> Unit,
    onToggleFavorite: () -> Unit,
    onDismiss: () -> Unit,
) {
    val efforts = effortsFor(catalog, model)
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Agent") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
                    PROVIDERS.forEachIndexed { i, info ->
                        SegmentedButton(
                            selected = provider == info.id,
                            onClick = { onProvider(info.id) },
                            shape = SegmentedButtonDefaults.itemShape(i, PROVIDERS.size),
                        ) { Text(info.name) }
                    }
                }
                OptionPicker(
                    label = "Model",
                    options = catalog.models,
                    selected = model,
                    error = modelsError,
                    onSelect = onModel,
                    modifier = Modifier.fillMaxWidth(),
                )
                if (efforts.isNotEmpty()) {
                    OptionPicker(
                        label = "Effort",
                        options = efforts,
                        selected = effort,
                        error = null,
                        onSelect = onEffort,
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }
        },
        dismissButton = {
            TextButton(onClick = onToggleFavorite) {
                Icon(
                    if (favorite) Icons.Default.Star else Icons.Default.StarBorder,
                    contentDescription = null,
                    Modifier.size(18.dp),
                )
                Text(if (favorite) "  Saved" else "  Save")
            }
        },
        confirmButton = { TextButton(onClick = onDismiss) { Text("Done") } },
    )
}

/** Model or effort dropdown; the first entry keeps the agent's own default. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun OptionPicker(
    label: String,
    options: List<CatalogOption>,
    selected: String,
    error: String?,
    onSelect: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    var expanded by remember { mutableStateOf(false) }
    val defaultName = options.firstOrNull { it.default }?.name
    val defaultLabel = if (defaultName != null) "Default ($defaultName)" else "Default"
    val current = options.firstOrNull { it.id == selected }?.name ?: defaultLabel
    ExposedDropdownMenuBox(expanded = expanded, onExpandedChange = { expanded = it }, modifier = modifier) {
        OutlinedTextField(
            value = current,
            onValueChange = {},
            readOnly = true,
            singleLine = true,
            textStyle = MaterialTheme.typography.bodyMedium,
            label = { Text(label) },
            supportingText = error?.let { { Text("Could not load models: $it") } },
            isError = error != null,
            trailingIcon = { ExposedDropdownMenuDefaults.TrailingIcon(expanded = expanded) },
            modifier = Modifier
                .fillMaxWidth()
                .menuAnchor(ExposedDropdownMenuAnchorType.PrimaryNotEditable),
        )
        ExposedDropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
            DropdownMenuItem(
                text = { Text(defaultLabel) },
                onClick = {
                    onSelect("")
                    expanded = false
                },
            )
            options.forEach { m ->
                DropdownMenuItem(
                    text = {
                        Column {
                            Text(m.name)
                            m.description?.takeIf { it.isNotBlank() }?.let {
                                Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            }
                        }
                    },
                    onClick = {
                        onSelect(m.id)
                        expanded = false
                    },
                )
            }
        }
    }
}
