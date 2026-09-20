package com.tohutohu.herdrcompanion.ui.markdown

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.Layout
import androidx.compose.ui.layout.Placeable
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.LinkAnnotation
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLinkStyles
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.withLink
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.Constraints
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.unit.takeOrElse

/**
 * Renders a Markdown subset (see [parseMarkdown]).
 *
 * Headings stay close to body size to keep the transcript dense; their `#`
 * marks are kept visible instead, so the outline is still readable.
 */
@Composable
fun MarkdownText(
    text: String,
    modifier: Modifier = Modifier,
    style: TextStyle = MaterialTheme.typography.bodyMedium,
) {
    val blocks = remember(text) { parseMarkdown(text) }
    // No fillMaxWidth: user message bubbles should still hug short text.
    Column(modifier, verticalArrangement = Arrangement.spacedBy(2.dp)) {
        blocks.forEachIndexed { index, block ->
            MarkdownBlock(block, style, previous = blocks.getOrNull(index - 1))
        }
    }
}

@Composable
private fun MarkdownBlock(block: MdBlock, style: TextStyle, previous: MdBlock?) {
    // Headings breathe a little; items of one list stay tight.
    val gap = when {
        previous == null -> 0.dp
        block is MdBlock.Heading -> 8.dp
        block is MdBlock.Rule -> 0.dp
        block is MdBlock.ListItem && previous is MdBlock.ListItem -> 0.dp
        else -> 4.dp
    }
    val top = Modifier.padding(top = gap)
    when (block) {
        is MdBlock.Paragraph -> Text(block.spans.annotated(), style = style, modifier = top)

        is MdBlock.Heading -> {
            val base = style.fontSize.takeOrElse { 14.sp }
            val heading = style.copy(
                fontSize = when (block.level) {
                    1 -> base * 1.15f
                    2 -> base * 1.08f
                    else -> base
                },
                fontWeight = FontWeight.Bold,
            )
            val markStyle = SpanStyle(
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                fontFamily = FontFamily.Monospace,
                fontWeight = FontWeight.Normal,
            )
            val content = block.spans.annotated()
            val marks = buildAnnotatedString {
                withStyle(markStyle) { append("#".repeat(block.level)) }
                append(" ")
                append(content)
            }
            Text(marks, style = heading, modifier = top)
        }

        is MdBlock.ListItem -> Row(top.padding(start = (block.depth * 14).dp)) {
            Text(
                block.marker,
                style = style,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier
                    .widthIn(min = if (block.marker.length > 1) 22.dp else 14.dp)
                    .padding(end = 4.dp),
            )
            Text(block.spans.annotated(), style = style)
        }

        is MdBlock.CodeBlock -> Box(
            top
                .fillMaxWidth()
                .clip(RoundedCornerShape(6.dp))
                .background(MaterialTheme.colorScheme.surfaceVariant)
                .horizontalScroll(rememberScrollState())
                .padding(horizontal = 8.dp, vertical = 6.dp),
        ) {
            HighlightedCodeText(
                code = block.code,
                language = block.language,
                style = MaterialTheme.typography.bodySmall.copy(color = MaterialTheme.colorScheme.onSurfaceVariant),
            )
        }

        is MdBlock.Quote -> Row(top.fillMaxWidth().height(IntrinsicSize.Min)) {
            Box(
                Modifier
                    .width(3.dp)
                    .fillMaxHeight()
                    .clip(RoundedCornerShape(2.dp))
                    .background(MaterialTheme.colorScheme.outlineVariant),
            )
            Column(
                Modifier.padding(start = 8.dp),
                verticalArrangement = Arrangement.spacedBy(2.dp),
            ) {
                block.blocks.forEachIndexed { index, inner ->
                    MarkdownBlock(
                        inner,
                        style.copy(color = MaterialTheme.colorScheme.onSurfaceVariant),
                        previous = block.blocks.getOrNull(index - 1),
                    )
                }
            }
        }

        is MdBlock.Table -> MarkdownTable(block, style, top)

        MdBlock.Rule -> HorizontalDivider(Modifier.padding(vertical = 4.dp))
    }
}

@Composable
private fun MarkdownTable(block: MdBlock.Table, style: TextStyle, modifier: Modifier) {
    val scrollState = rememberScrollState()
    val rows = remember(block) {
        buildList {
            add(block.header to true)
            block.rows.forEach { add(it to false) }
        }
    }

    Box(modifier.fillMaxWidth().horizontalScroll(scrollState)) {
        Layout(
            content = {
                rows.forEach { (row, header) ->
                    row.forEachIndexed { column, spans ->
                        MarkdownTableCell(
                            spans = spans,
                            style = style,
                            header = header,
                            alignment = block.alignments.getOrElse(column) { MdTableAlignment.Start },
                        )
                    }
                }
            },
            modifier = Modifier.padding(vertical = 4.dp),
        ) { measurables, constraints ->
            val columnCount = block.alignments.size
            val minCellWidth = 72.dp.roundToPx()
            val maxCellWidth = 240.dp.roundToPx()
            val columnWidths = IntArray(columnCount) { minCellWidth }

            var childIndex = 0
            rows.forEach { (row, _) ->
                row.forEachIndexed { column, _ ->
                    val naturalWidth = measurables[childIndex++].maxIntrinsicWidth(Constraints.Infinity)
                    columnWidths[column] = maxOf(
                        columnWidths[column],
                        naturalWidth.coerceAtMost(maxCellWidth),
                    )
                }
            }

            val rowHeights = IntArray(rows.size)
            childIndex = 0
            rows.forEachIndexed { rowIndex, (row, _) ->
                row.forEachIndexed { column, _ ->
                    val naturalHeight = measurables[childIndex++].maxIntrinsicHeight(columnWidths[column])
                    rowHeights[rowIndex] = maxOf(
                        rowHeights[rowIndex],
                        naturalHeight,
                    )
                }
            }

            val placeables = ArrayList<Placeable>(measurables.size)
            childIndex = 0
            rows.forEachIndexed { rowIndex, (row, _) ->
                row.forEachIndexed { column, _ ->
                    val placeable = measurables[childIndex++].measure(
                        Constraints.fixed(columnWidths[column], rowHeights[rowIndex]),
                    )
                    placeables += placeable
                }
            }

            val tableWidth = columnWidths.sum().coerceAtLeast(constraints.minWidth)
            val tableHeight = rowHeights.sum()
            layout(tableWidth, tableHeight) {
                var y = 0
                var placeableIndex = 0
                rows.forEachIndexed { rowIndex, (row, _) ->
                    var x = 0
                    row.forEachIndexed { column, _ ->
                        placeables[placeableIndex++].placeRelative(x, y)
                        x += columnWidths[column]
                    }
                    y += rowHeights[rowIndex]
                }
            }
        }
    }
}

@Composable
private fun MarkdownTableCell(
    spans: List<MdSpan>,
    style: TextStyle,
    header: Boolean,
    alignment: MdTableAlignment,
) {
    val colors = MaterialTheme.colorScheme
    Box(
        Modifier
            .background(if (header) colors.surfaceVariant else Color.Transparent)
            .border(1.dp, colors.outlineVariant)
            .padding(horizontal = 8.dp, vertical = 6.dp),
    ) {
        Text(
            text = spans.annotated(),
            style = style.copy(
                fontWeight = if (header) FontWeight.Bold else style.fontWeight,
                textAlign = when (alignment) {
                    MdTableAlignment.Start -> TextAlign.Start
                    MdTableAlignment.Center -> TextAlign.Center
                    MdTableAlignment.End -> TextAlign.End
                },
            ),
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

@Composable
private fun List<MdSpan>.annotated(): AnnotatedString {
    val codeBackground = MaterialTheme.colorScheme.surfaceVariant
    val linkColor = MaterialTheme.colorScheme.primary
    return buildAnnotatedString {
        this@annotated.forEach { span ->
            val spanStyle = SpanStyle(
                fontWeight = if (span.bold) FontWeight.Bold else null,
                fontStyle = if (span.italic) FontStyle.Italic else null,
                fontFamily = if (span.code) FontFamily.Monospace else null,
                background = if (span.code) codeBackground else Color.Unspecified,
                textDecoration = if (span.strike) TextDecoration.LineThrough else null,
            )
            val url = span.link
            if (url == null) {
                withStyle(spanStyle) { append(span.text) }
            } else {
                val styles = TextLinkStyles(
                    style = spanStyle.copy(color = linkColor, textDecoration = TextDecoration.Underline),
                )
                withLink(LinkAnnotation.Url(url, styles)) { append(span.text) }
            }
        }
    }
}
