package com.tohutohu.herdrmobile.ui.detail

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CheckboxDefaults
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.RadioButton
import androidx.compose.material3.RadioButtonDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.ReadOnlyComposable
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrmobile.data.api.AnswerDto
import com.tohutohu.herdrmobile.data.api.InteractionDto
import com.tohutohu.herdrmobile.data.api.InteractionResponseDto
import com.tohutohu.herdrmobile.data.api.QuestionDto

/** Sentinel used as the "Other" option key. */
private const val OTHER = "__herdr_other__"

/**
 * Colors inside the card come only from the card's container/content pair. With
 * dynamic color the pending card's tertiaryContainer can be light even in dark
 * theme, so other roles (primary, tertiary, onSurfaceVariant) may not contrast
 * with it. The content color is used as the accent and the container color as
 * the color drawn on top of the accent.
 */
private val LocalCardContainer = staticCompositionLocalOf { Color.Unspecified }

private val Muted: Color
    @Composable @ReadOnlyComposable get() = LocalContentColor.current.copy(alpha = 0.75f)

private val Accent: Color
    @Composable @ReadOnlyComposable get() = LocalContentColor.current

private val OnAccent: Color
    @Composable @ReadOnlyComposable get() = LocalCardContainer.current

@Composable
fun InteractionCard(
    interaction: InteractionDto,
    enabled: Boolean,
    onRespond: (InteractionResponseDto) -> Unit,
    onOpenTerminal: () -> Unit,
) {
    val pending = interaction.isPending
    val container = if (pending) MaterialTheme.colorScheme.tertiaryContainer else MaterialTheme.colorScheme.surfaceVariant
    Card(
        colors = CardDefaults.cardColors(
            containerColor = container,
            contentColor = if (pending) MaterialTheme.colorScheme.onTertiaryContainer else MaterialTheme.colorScheme.onSurfaceVariant,
        ),
        modifier = Modifier.fillMaxWidth(),
    ) {
        CompositionLocalProvider(LocalCardContainer provides container) {
            Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                interaction.title?.let { Text(it, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.Bold) }
                when {
                    !pending -> AnsweredSummary(interaction)
                    !interaction.supported -> Unsupported(onOpenTerminal)
                    interaction.type == "approval" -> Approval(interaction, enabled, onRespond)
                    interaction.type == "questions" -> Questions(interaction, enabled, onRespond)
                    else -> Unsupported(onOpenTerminal)
                }
            }
        }
    }
}

@Composable
private fun AnsweredSummary(interaction: InteractionDto) {
    interaction.questions.forEach { Text("• ${it.question}", style = MaterialTheme.typography.bodySmall) }
    interaction.detail?.let { Text(it, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall) }
    val summary = when (interaction.state) {
        "answered" -> interaction.answer?.takeIf { it.isNotBlank() }?.let { "Answered: $it" } ?: "Answered"
        else -> "No longer waiting for an answer"
    }
    Text(summary, style = MaterialTheme.typography.bodySmall, color = Muted)
}

@Composable
private fun Unsupported(onOpenTerminal: () -> Unit) {
    Text("This interaction isn't supported yet.")
    OutlinedButton(onClick = onOpenTerminal, colors = outlinedButtonColors()) { Text("Open Terminal") }
}

@Composable
private fun filledButtonColors() = ButtonDefaults.buttonColors(
    containerColor = Accent,
    contentColor = OnAccent,
    disabledContainerColor = LocalContentColor.current.copy(alpha = 0.12f),
    disabledContentColor = LocalContentColor.current.copy(alpha = 0.38f),
)

@Composable
private fun outlinedButtonColors() = ButtonDefaults.outlinedButtonColors(
    contentColor = LocalContentColor.current,
    disabledContentColor = LocalContentColor.current.copy(alpha = 0.38f),
)

@Composable
private fun Approval(interaction: InteractionDto, enabled: Boolean, onRespond: (InteractionResponseDto) -> Unit) {
    interaction.detail?.let {
        Text(it, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall)
    }
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        interaction.decisions.forEach { d ->
            val label = when (d) {
                "approve" -> "Approve"
                "approve_session" -> "Always"
                "deny" -> "Deny"
                else -> d
            }
            val onClick = { onRespond(InteractionResponseDto(interactionId = interaction.id, decision = d)) }
            if (d == "deny") {
                OutlinedButton(onClick = onClick, enabled = enabled, colors = outlinedButtonColors()) { Text(label) }
            } else {
                Button(onClick = onClick, enabled = enabled, colors = filledButtonColors()) { Text(label) }
            }
        }
    }
}

@Composable
private fun Questions(interaction: InteractionDto, enabled: Boolean, onRespond: (InteractionResponseDto) -> Unit) {
    // question id -> selected labels (OTHER marks the free-text choice)
    val selections = remember(interaction.id) { mutableStateMapOf<String, Set<String>>() }
    val otherTexts = remember(interaction.id) { mutableStateMapOf<String, String>() }

    interaction.questions.forEach { q ->
        QuestionView(
            q,
            selected = selections[q.id].orEmpty(),
            otherText = otherTexts[q.id].orEmpty(),
            onSelect = { selections[q.id] = it },
            onOtherText = { otherTexts[q.id] = it },
        )
    }

    fun answerFor(q: QuestionDto): AnswerDto? {
        val sel = selections[q.id].orEmpty()
        val other = otherTexts[q.id].orEmpty().trim()
        val labels = sel.filter { it != OTHER }
        val useOther = OTHER in sel || q.type == "text"
        return when {
            q.type == "multiselect" -> if (labels.isEmpty() && !(useOther && other.isNotEmpty())) null
            else AnswerDto(selected = labels, text = other.takeIf { useOther && it.isNotEmpty() })
            useOther -> if (other.isEmpty()) null else AnswerDto(text = other)
            labels.isNotEmpty() -> AnswerDto(selected = labels.take(1))
            else -> null
        }
    }

    val answers = interaction.questions.associate { it.id to answerFor(it) }
    val complete = answers.values.all { it != null }
    Button(
        enabled = enabled && complete,
        colors = filledButtonColors(),
        onClick = {
            onRespond(
                InteractionResponseDto(
                    interactionId = interaction.id,
                    answers = answers.mapValues { it.value!! },
                ),
            )
        },
    ) { Text("Answer") }
}

@Composable
private fun QuestionView(
    q: QuestionDto,
    selected: Set<String>,
    otherText: String,
    onSelect: (Set<String>) -> Unit,
    onOtherText: (String) -> Unit,
) {
    val multi = q.type == "multiselect"
    Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
        q.header?.takeIf { it.isNotBlank() }?.let {
            Text(it, style = MaterialTheme.typography.labelMedium, fontWeight = FontWeight.Bold, color = Muted)
        }
        Text(q.question, style = MaterialTheme.typography.bodyLarge)

        fun toggle(key: String) {
            onSelect(
                if (multi) {
                    if (key in selected) selected - key else selected + key
                } else {
                    setOf(key)
                },
            )
        }

        val choices = q.options.map { it.label to it.description } +
            if (q.allowOther && q.type != "text") listOf(OTHER to null) else emptyList()
        choices.forEach { (key, description) ->
            Row(
                verticalAlignment = Alignment.CenterVertically,
                modifier = Modifier
                    .fillMaxWidth()
                    .clickable { toggle(key) },
            ) {
                if (multi) {
                    Checkbox(
                        checked = key in selected,
                        onCheckedChange = { toggle(key) },
                        colors = CheckboxDefaults.colors(
                            checkedColor = Accent,
                            checkmarkColor = OnAccent,
                            uncheckedColor = LocalContentColor.current.copy(alpha = 0.6f),
                        ),
                    )
                } else {
                    RadioButton(
                        selected = key in selected,
                        onClick = { toggle(key) },
                        colors = RadioButtonDefaults.colors(
                            selectedColor = Accent,
                            unselectedColor = LocalContentColor.current.copy(alpha = 0.6f),
                        ),
                    )
                }
                Column {
                    Text(if (key == OTHER) "Other…" else key)
                    description?.takeIf { it.isNotBlank() }?.let {
                        Text(it, style = MaterialTheme.typography.bodySmall, color = Muted)
                    }
                }
            }
        }
        if (OTHER in selected || q.type == "text") {
            OutlinedTextField(
                value = otherText,
                onValueChange = onOtherText,
                placeholder = { Text("Your answer") },
                colors = OutlinedTextFieldDefaults.colors(
                    focusedTextColor = LocalContentColor.current,
                    unfocusedTextColor = LocalContentColor.current,
                    cursorColor = Accent,
                    focusedBorderColor = Accent,
                    unfocusedBorderColor = LocalContentColor.current.copy(alpha = 0.4f),
                    focusedPlaceholderColor = Muted,
                    unfocusedPlaceholderColor = Muted,
                ),
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}
