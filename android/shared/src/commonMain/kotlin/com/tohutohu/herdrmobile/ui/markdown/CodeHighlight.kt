package com.tohutohu.herdrmobile.ui.markdown

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withStyle

/** The small set of token categories used by the code renderer. */
enum class CodeTokenKind {
    Plain,
    Keyword,
    Type,
    String,
    Number,
    Comment,
    Function,
    Property,
    Operator,
    Punctuation,
    Annotation,
    Tag,
    Attribute,
    Constant,
}

data class CodeToken(
    val text: String,
    val kind: CodeTokenKind = CodeTokenKind.Plain,
)

private val SLASH_COMMENT_LANGUAGES = setOf(
    "c", "cpp", "csharp", "dart", "go", "groovy", "java", "javascript", "kotlin",
    "php", "rust", "scala", "swift", "typescript", "generic",
)

private val HASH_COMMENT_LANGUAGES = setOf(
    "bash", "docker", "make", "perl", "python", "ruby", "shell", "toml", "yaml", "zsh",
)

private val MARKUP_LANGUAGES = setOf("html", "jsx", "tsx", "xml", "xhtml", "svg")
private val JSON_LANGUAGES = setOf("json", "jsonc")
private val YAML_LANGUAGES = setOf("yaml")
private val TYPE_LANGUAGES = setOf(
    "c", "cpp", "csharp", "dart", "go", "java", "javascript", "kotlin", "php", "rust",
    "scala", "swift", "typescript", "generic",
)
private val PROPERTY_LANGUAGES = JSON_LANGUAGES + YAML_LANGUAGES + setOf("toml")
private val PREPROCESSOR_LANGUAGES = setOf("c", "cpp", "csharp")

private val COMMON_KEYWORDS = setOf(
    "as", "async", "await", "break", "case", "catch", "class", "const", "continue", "data",
    "default", "defer", "do", "else", "enum", "export", "extends", "finally", "for", "fun",
    "fn", "from", "function", "if", "implements", "import", "in", "interface", "internal",
    "is", "let", "match", "mod", "new", "object", "open", "override", "package", "private",
    "protected", "public", "range", "return", "sealed", "select", "self", "static", "struct",
    "super", "switch", "throw", "trait", "try", "type", "typeof", "use", "val", "var", "when",
    "while", "with", "yield",
)

private val LANGUAGE_KEYWORDS = mapOf(
    "bash" to setOf("case", "do", "done", "elif", "else", "esac", "fi", "for", "function", "if", "in", "select", "then", "until", "while"),
    "c" to setOf("auto", "break", "case", "const", "continue", "default", "do", "else", "enum", "extern", "for", "goto", "if", "inline", "register", "restrict", "return", "sizeof", "static", "struct", "switch", "typedef", "union", "volatile", "while"),
    "cpp" to setOf("alignas", "alignof", "auto", "break", "case", "catch", "class", "const", "constexpr", "continue", "co_await", "co_return", "co_yield", "default", "delete", "do", "else", "enum", "explicit", "export", "extern", "for", "friend", "goto", "if", "inline", "namespace", "new", "noexcept", "nullptr", "operator", "private", "protected", "public", "return", "sizeof", "static", "struct", "switch", "template", "this", "throw", "try", "typedef", "typename", "union", "using", "virtual", "void", "volatile", "while"),
    "csharp" to setOf("abstract", "as", "async", "await", "base", "break", "case", "catch", "checked", "class", "const", "continue", "default", "delegate", "do", "else", "enum", "event", "explicit", "extern", "finally", "fixed", "for", "foreach", "goto", "if", "implicit", "in", "interface", "internal", "is", "lock", "namespace", "new", "null", "object", "operator", "out", "override", "params", "private", "protected", "public", "readonly", "record", "ref", "return", "sealed", "sizeof", "stackalloc", "static", "struct", "switch", "this", "throw", "try", "typeof", "unchecked", "unsafe", "using", "virtual", "void", "volatile", "while", "with", "yield"),
    "go" to setOf("break", "case", "chan", "const", "continue", "default", "defer", "else", "fallthrough", "for", "func", "go", "goto", "if", "import", "interface", "map", "package", "range", "return", "select", "struct", "switch", "type", "var"),
    "java" to setOf("abstract", "assert", "break", "case", "catch", "class", "const", "continue", "default", "do", "else", "enum", "extends", "final", "finally", "for", "if", "implements", "import", "instanceof", "interface", "native", "new", "package", "private", "protected", "public", "return", "static", "strictfp", "super", "switch", "synchronized", "this", "throw", "throws", "transient", "try", "volatile", "while"),
    "javascript" to setOf("async", "await", "break", "case", "catch", "class", "const", "continue", "debugger", "default", "delete", "do", "else", "export", "extends", "finally", "for", "from", "function", "get", "if", "import", "in", "instanceof", "let", "new", "of", "return", "set", "static", "super", "switch", "this", "throw", "try", "typeof", "var", "void", "while", "with", "yield"),
    "kotlin" to setOf("abstract", "actual", "annotation", "as", "break", "by", "catch", "class", "companion", "const", "constructor", "continue", "crossinline", "data", "delegate", "do", "dynamic", "else", "enum", "expect", "external", "final", "finally", "for", "fun", "if", "import", "in", "infix", "init", "inline", "inner", "interface", "internal", "is", "lateinit", "noinline", "object", "open", "operator", "out", "override", "package", "private", "protected", "public", "reified", "return", "sealed", "select", "suspend", "tailrec", "this", "throw", "try", "typealias", "typeof", "val", "var", "vararg", "when", "where", "while"),
    "php" to setOf("abstract", "and", "array", "as", "break", "callable", "case", "catch", "class", "clone", "const", "continue", "declare", "default", "die", "do", "echo", "else", "elseif", "empty", "enddeclare", "endfor", "endforeach", "endif", "endswitch", "endwhile", "extends", "final", "finally", "fn", "for", "foreach", "function", "global", "goto", "if", "implements", "include", "instanceof", "insteadof", "interface", "isset", "list", "match", "namespace", "new", "or", "private", "protected", "public", "readonly", "require", "return", "static", "switch", "throw", "trait", "try", "unset", "use", "var", "while", "xor", "yield"),
    "python" to setOf("and", "as", "assert", "async", "await", "break", "case", "class", "continue", "def", "del", "elif", "else", "except", "finally", "for", "from", "global", "if", "import", "in", "is", "lambda", "match", "nonlocal", "not", "or", "pass", "raise", "return", "try", "while", "with", "yield"),
    "rust" to setOf("as", "async", "await", "break", "const", "continue", "crate", "dyn", "else", "enum", "extern", "false", "fn", "for", "if", "impl", "in", "let", "loop", "match", "mod", "move", "mut", "pub", "ref", "return", "self", "Self", "static", "struct", "super", "trait", "true", "type", "unsafe", "use", "where", "while"),
    "sql" to setOf("all", "alter", "and", "as", "asc", "begin", "by", "case", "commit", "create", "delete", "desc", "distinct", "drop", "else", "end", "exists", "from", "group", "having", "in", "inner", "insert", "into", "is", "join", "left", "like", "limit", "not", "null", "offset", "on", "or", "order", "outer", "over", "partition", "primary", "right", "rollback", "select", "set", "table", "then", "union", "unique", "update", "using", "values", "when", "where", "with"),
    "swift" to setOf("associatedtype", "as", "break", "case", "catch", "class", "continue", "default", "defer", "deinit", "do", "else", "enum", "extension", "fallthrough", "fileprivate", "for", "func", "guard", "if", "import", "in", "indirect", "init", "inout", "internal", "is", "let", "mutating", "nil", "open", "operator", "override", "private", "protocol", "public", "repeat", "required", "return", "self", "static", "struct", "subscript", "super", "switch", "throw", "throws", "try", "typealias", "unowned", "var", "weak", "where", "while"),
    "typescript" to setOf("abstract", "any", "as", "asserts", "async", "await", "bigint", "boolean", "break", "case", "catch", "class", "const", "constructor", "continue", "declare", "default", "delete", "do", "else", "enum", "export", "extends", "false", "finally", "for", "from", "function", "if", "implements", "import", "in", "infer", "instanceof", "interface", "is", "keyof", "let", "module", "namespace", "never", "new", "null", "number", "object", "of", "override", "private", "protected", "public", "readonly", "return", "satisfies", "static", "string", "super", "switch", "symbol", "this", "throw", "true", "try", "type", "typeof", "undefined", "unknown", "var", "void", "while", "with", "yield"),
)

private val COMMON_TYPES = setOf(
    "Any", "Array", "BigInt", "Boolean", "Byte", "Char", "Collection", "Double", "Float", "HashMap",
    "Int", "Integer", "List", "Long", "Map", "Number", "Object", "Set", "Short", "String", "Unit",
    "UInt", "ULong", "UShort", "UByte", "bool", "boolean", "byte", "char", "double", "float", "int",
    "long", "number", "object", "short", "string", "void",
)

private val CONSTANTS = setOf(
    "False", "NaN", "None", "Null", "NULL", "Nothing", "True", "Undefined", "Unit", "false", "nil", "null", "true", "undefined",
)

private val OPERATOR_LEXEMES = listOf(
    "===", "!==", ">>>", "...", "**=", "&&=", "||=", "??=", "<<=", ">>=", "=>", "->", "::", "?.", "??", "&&", "||", "==", "!=", "<=", ">=", "++", "--", "+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "**", "//", "<<", ">>", "..",
)

/**
 * Highlights common source formats without pulling a platform-specific editor
 * library into the shared UI. It is deliberately a lexer rather than a full
 * parser: malformed or partial agent output still renders as ordinary text.
 */
fun highlightCode(code: String, language: String? = null): List<CodeToken> {
    if (code.isEmpty()) return emptyList()

    val lang = normalizeLanguage(language)
    if (lang in setOf("plain", "text", "plaintext", "txt", "log")) return listOf(CodeToken(code))

    val tokens = mutableListOf<CodeToken>()
    fun add(start: Int, end: Int, kind: CodeTokenKind) {
        if (end > start) tokens += CodeToken(code.substring(start, end), kind)
    }

    fun nextNonWhitespace(from: Int): Char? {
        var index = from
        while (index < code.length && code[index].isWhitespace()) index++
        return code.getOrNull(index)
    }

    fun previousNonWhitespace(from: Int): Char? {
        var index = from - 1
        while (index >= 0 && code[index].isWhitespace()) index--
        return code.getOrNull(index)
    }

    fun atLineStart(index: Int): Boolean {
        var cursor = index - 1
        while (cursor >= 0 && code[cursor] != '\n') {
            if (!code[cursor].isWhitespace()) return false
            cursor--
        }
        return true
    }

    fun identifierStart(c: Char): Boolean = c == '_' || c == '$' || c.isLetter()
    fun identifierPart(c: Char): Boolean = identifierStart(c) || c.isDigit()

    fun quotedEnd(start: Int, quote: Char): Int {
        val triple = quote != '`' && code.startsWith(quote.toString().repeat(3), start)
        val delimiterLength = if (triple) 3 else 1
        var index = start + delimiterLength
        while (index < code.length) {
            if (code[index] == '\\') {
                index += 2
                continue
            }
            if (triple && code.startsWith(quote.toString().repeat(3), index)) return index + 3
            if (!triple && code[index] == quote) return index + 1
            if (!triple && quote != '`' && code[index] == '\n') return index
            index++
        }
        return code.length
    }

    fun markupTagStart(index: Int): Boolean {
        val next = code.getOrNull(index + 1) ?: return false
        return next.isLetter() || next == '/' || next == '!' || next == '?'
    }

    fun addMarkupTag(start: Int): Int {
        var index = start
        add(index, index + 1, CodeTokenKind.Punctuation)
        index++
        if (code.getOrNull(index) == '/') {
            add(index, index + 1, CodeTokenKind.Punctuation)
            index++
        }
        while (index < code.length && code[index].isWhitespace()) index++
        val tagStart = index
        while (index < code.length && (code[index].isLetterOrDigit() || code[index] in "-_:.")) index++
        if (index > tagStart) add(tagStart, index, CodeTokenKind.Tag)

        while (index < code.length && code[index] != '>') {
            if (code[index].isWhitespace()) {
                val whitespaceStart = index
                while (index < code.length && code[index].isWhitespace()) index++
                add(whitespaceStart, index, CodeTokenKind.Plain)
                continue
            }
            if (code[index] == '/' && code.getOrNull(index + 1) == '>') {
                add(index, index + 1, CodeTokenKind.Punctuation)
                index++
                continue
            }
            if (identifierStart(code[index])) {
                val attributeStart = index
                while (index < code.length && (identifierPart(code[index]) || code[index] in "-:.")) index++
                add(attributeStart, index, CodeTokenKind.Attribute)
                if (code.getOrNull(index) == '=') {
                    add(index, index + 1, CodeTokenKind.Operator)
                    index++
                    val quote = code.getOrNull(index)
                    if (quote == '\'' || quote == '"') {
                        val end = quotedEnd(index, quote)
                        add(index, end, CodeTokenKind.String)
                        index = end
                    }
                }
                continue
            }
            add(index, index + 1, CodeTokenKind.Punctuation)
            index++
        }
        if (code.getOrNull(index) == '>') {
            add(index, index + 1, CodeTokenKind.Punctuation)
            index++
        }
        return index
    }

    val isMarkup = lang in MARKUP_LANGUAGES
    val isPropertyLanguage = lang in PROPERTY_LANGUAGES
    val keywords = COMMON_KEYWORDS + LANGUAGE_KEYWORDS.getOrElse(lang) { emptySet() }
    val types = COMMON_TYPES
    val slashComments = lang in SLASH_COMMENT_LANGUAGES
    val hashComments = lang in HASH_COMMENT_LANGUAGES
    val dashComments = lang == "sql"
    val lineComment = { start: Int ->
        var end = start
        while (end < code.length && code[end] != '\n') end++
        end
    }

    var index = 0
    while (index < code.length) {
        val c = code[index]
        when {
            c.isWhitespace() -> {
                val start = index
                while (index < code.length && code[index].isWhitespace()) index++
                add(start, index, CodeTokenKind.Plain)
            }

            isMarkup && c == '<' && code.startsWith("<!--", index) -> {
                val end = (code.indexOf("-->", index + 4).takeIf { it >= 0 } ?: code.length) +
                    if (code.indexOf("-->", index + 4) >= 0) 3 else 0
                add(index, end, CodeTokenKind.Comment)
                index = end
            }

            isMarkup && c == '<' && markupTagStart(index) -> {
                index = addMarkupTag(index)
            }

            c == '/' && code.getOrNull(index + 1) == '*' -> {
                val close = code.indexOf("*/", index + 2)
                val end = if (close < 0) code.length else close + 2
                add(index, end, CodeTokenKind.Comment)
                index = end
            }

            slashComments && c == '/' && code.getOrNull(index + 1) == '/' -> {
                val end = lineComment(index)
                add(index, end, CodeTokenKind.Comment)
                index = end
            }

            dashComments && c == '-' && code.getOrNull(index + 1) == '-' -> {
                val end = lineComment(index)
                add(index, end, CodeTokenKind.Comment)
                index = end
            }

            hashComments && c == '#' -> {
                val end = lineComment(index)
                add(index, end, CodeTokenKind.Comment)
                index = end
            }

            c == '#' && lang in PREPROCESSOR_LANGUAGES && atLineStart(index) -> {
                add(index, index + 1, CodeTokenKind.Annotation)
                index++
            }

            c == '@' && identifierStart(code.getOrNull(index + 1) ?: ' ') -> {
                val start = index++
                while (index < code.length && identifierPart(code[index])) index++
                add(start, index, CodeTokenKind.Annotation)
            }

            c == '\'' || c == '"' || c == '`' -> {
                val end = quotedEnd(index, c)
                val kind = if (isPropertyLanguage && nextNonWhitespace(end) == ':') CodeTokenKind.Property else CodeTokenKind.String
                add(index, end, kind)
                index = end
            }

            c.isDigit() || (c == '.' && code.getOrNull(index + 1)?.isDigit() == true) -> {
                val start = index
                index++
                while (index < code.length && (code[index].isLetterOrDigit() || code[index] in "._+-")) index++
                add(start, index, CodeTokenKind.Number)
            }

            identifierStart(c) -> {
                val start = index
                index++
                while (index < code.length && identifierPart(code[index])) index++
                val word = code.substring(start, index)
                val next = nextNonWhitespace(index)
                val previous = previousNonWhitespace(start)
                val kind = when {
                    word in keywords -> CodeTokenKind.Keyword
                    word in CONSTANTS -> CodeTokenKind.Constant
                    word in types || (word.firstOrNull()?.isUpperCase() == true && lang in TYPE_LANGUAGES) -> CodeTokenKind.Type
                    isPropertyLanguage && next == ':' -> CodeTokenKind.Property
                    next == '(' -> CodeTokenKind.Function
                    previous == '.' -> CodeTokenKind.Property
                    else -> CodeTokenKind.Plain
                }
                add(start, index, kind)
            }

            else -> {
                val operator = OPERATOR_LEXEMES.firstOrNull { code.startsWith(it, index) }
                if (operator != null) {
                    add(index, index + operator.length, CodeTokenKind.Operator)
                    index += operator.length
                } else {
                    val kind = if (c in "(){}[];,.:") CodeTokenKind.Punctuation else CodeTokenKind.Operator
                    add(index, index + 1, kind)
                    index++
                }
            }
        }
    }
    return tokens
}

/** Highlights a complete source file and keeps multiline lexer state intact. */
fun highlightCodeLines(code: String, language: String? = null): List<List<CodeToken>> {
    val lines = mutableListOf<MutableList<CodeToken>>(mutableListOf())
    for (token in highlightCode(code, language)) {
        var start = 0
        while (start <= token.text.length) {
            val newline = token.text.indexOf('\n', start)
            if (newline < 0) {
                if (start < token.text.length) lines.last() += CodeToken(token.text.substring(start), token.kind)
                break
            }
            if (newline > start) lines.last() += CodeToken(token.text.substring(start, newline), token.kind)
            lines.add(mutableListOf())
            start = newline + 1
        }
    }
    return lines
}

/** Maps a file name to the language aliases understood by [highlightCode]. */
fun languageForPath(path: String): String? {
    val name = path.substringAfterLast('/').lowercase()
    return when {
        name == "dockerfile" -> "docker"
        name == "makefile" -> "make"
        name.endsWith(".gradle.kts") -> "kotlin"
        name.endsWith(".gradle") -> "groovy"
        else -> when (name.substringAfterLast('.', "")) {
            "bash", "sh" -> "bash"
            "c" -> "c"
            "cc", "cpp", "cxx", "h", "hh", "hpp", "hxx" -> "cpp"
            "cs" -> "csharp"
            "css" -> "css"
            "dart" -> "dart"
            "go" -> "go"
            "gvy", "groovy" -> "groovy"
            "htm", "html" -> "html"
            "java" -> "java"
            "js", "cjs", "mjs", "jsx" -> "javascript"
            "json", "jsonc" -> "json"
            "kt", "kts" -> "kotlin"
            "less" -> "less"
            "md", "markdown" -> "markdown"
            "php" -> "php"
            "pl", "pm" -> "perl"
            "py", "pyw" -> "python"
            "rb" -> "ruby"
            "rs" -> "rust"
            "scala" -> "scala"
            "scss" -> "scss"
            "sql" -> "sql"
            "swift" -> "swift"
            "toml" -> "toml"
            "ts", "mts", "cts", "tsx" -> "typescript"
            "txt", "log" -> "plain"
            "xml", "xsd", "xsl", "svg" -> "xml"
            "yaml", "yml" -> "yaml"
            else -> null
        }
    }
}

private fun normalizeLanguage(language: String?): String {
    val key = language.orEmpty().trim().lowercase().removePrefix("language-").removePrefix(".")
    return when (key) {
        "", "unknown" -> "generic"
        "kt", "kts" -> "kotlin"
        "js", "jsx", "mjs", "cjs" -> "javascript"
        "ts", "tsx", "mts", "cts" -> "typescript"
        "py", "pyw" -> "python"
        "rb" -> "ruby"
        "rs" -> "rust"
        "golang" -> "go"
        "c++", "cc", "cxx", "h", "hpp" -> "cpp"
        "cs", "c#" -> "csharp"
        "html", "htm" -> "html"
        "md", "markdown" -> "markdown"
        "sh", "shell", "zsh" -> "bash"
        "yml" -> "yaml"
        "text", "plaintext" -> "plain"
        else -> key
    }
}

@Composable
fun HighlightedCodeText(
    code: String,
    language: String? = null,
    modifier: Modifier = Modifier,
    style: TextStyle = MaterialTheme.typography.bodySmall,
) {
    val tokens = remember(code, language) { highlightCode(code, language) }
    HighlightedCodeLine(tokens, modifier, style)
}

@Composable
fun HighlightedCodeLine(
    tokens: List<CodeToken>,
    modifier: Modifier = Modifier,
    style: TextStyle = MaterialTheme.typography.bodySmall,
) {
    val colors = MaterialTheme.colorScheme
    val annotated = remember(tokens, colors) {
        buildAnnotatedString {
            tokens.forEach { token ->
                withStyle(token.kind.spanStyle(colors)) { append(token.text) }
            }
        }
    }
    Text(
        text = annotated,
        modifier = modifier,
        style = style.copy(fontFamily = FontFamily.Monospace),
        softWrap = false,
    )
}

private fun CodeTokenKind.spanStyle(colors: androidx.compose.material3.ColorScheme): SpanStyle = when (this) {
    CodeTokenKind.Keyword -> SpanStyle(color = colors.primary, fontWeight = FontWeight.SemiBold)
    CodeTokenKind.Type -> SpanStyle(color = colors.secondary, fontWeight = FontWeight.Medium)
    CodeTokenKind.String -> SpanStyle(color = colors.tertiary)
    CodeTokenKind.Number -> SpanStyle(color = colors.secondary)
    CodeTokenKind.Comment -> SpanStyle(color = colors.onSurfaceVariant.copy(alpha = 0.72f), fontStyle = FontStyle.Italic)
    CodeTokenKind.Function -> SpanStyle(color = colors.primary)
    CodeTokenKind.Property -> SpanStyle(color = colors.tertiary)
    CodeTokenKind.Operator -> SpanStyle(color = colors.onSurfaceVariant)
    CodeTokenKind.Punctuation -> SpanStyle(color = colors.onSurfaceVariant)
    CodeTokenKind.Annotation -> SpanStyle(color = colors.error)
    CodeTokenKind.Tag -> SpanStyle(color = colors.primary, fontWeight = FontWeight.SemiBold)
    CodeTokenKind.Attribute -> SpanStyle(color = colors.secondary)
    CodeTokenKind.Constant -> SpanStyle(color = colors.tertiary, fontWeight = FontWeight.Medium)
    CodeTokenKind.Plain -> SpanStyle(color = Color.Unspecified)
}
