package com.tohutohu.herdrcompanion.dictate

import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.platform.InterceptPlatformTextInput
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.PlatformTextInputMethodRequest
import com.tohutohu.herdrcompanion.container

/**
 * Declares every text field in [content] to Dictate as part of [project]'s
 * context. The token lives as long as this scope, so Dictate's dialog taking
 * focus away for a moment does not invalidate it.
 */
@Composable
fun DictateContextScope(project: String?, content: @Composable () -> Unit) {
    val context = LocalContext.current
    val container = context.container
    val token = remember { DictateContract.newToken() }
    val currentProject by rememberUpdatedState(project)
    DisposableEffect(token) {
        container.dictateFields.register(token) { currentProject }
        onDispose { container.dictateFields.unregister(token) }
    }
    val projects by container.projectVocabulary.projects.collectAsState()
    val revision = project?.let { projects[it]?.revision }
    LaunchedEffect(project, revision) { DictateContract.notifyInvalidated(context.applicationContext) }
    InterceptPlatformTextInput(
        interceptor = { request, nextHandler ->
            val declared = PlatformTextInputMethodRequest { outAttributes ->
                request.createInputConnection(outAttributes).also { DictateContract.declare(outAttributes, token) }
            }
            nextHandler.startInputMethod(declared)
        },
        content = content,
    )
}
