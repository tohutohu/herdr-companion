package com.tohutohu.herdrcompanion.ui.projects

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.toAwtImage
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.data.api.NotifyLevel
import com.tohutohu.herdrcompanion.data.api.ProjectDto
import com.tohutohu.herdrcompanion.data.api.ResidentTaskDto
import com.tohutohu.herdrcompanion.data.api.Status
import com.tohutohu.herdrcompanion.data.api.TaskState
import com.tohutohu.herdrcompanion.model.SessionUiModel
import com.tohutohu.herdrcompanion.ui.resident.ResidentBackend
import com.tohutohu.herdrcompanion.ui.sessions.SessionListItemUiState
import java.io.File
import java.lang.reflect.Proxy
import javax.imageio.ImageIO
import kotlin.test.assertEquals
import org.junit.Rule
import org.junit.Test

/** The Projects tab's screens on a phone-sized surface, without a gateway. */
class ProjectScreensTest {
    @get:Rule val rule = createComposeRule()

    private val backend = Proxy.newProxyInstance(ResidentBackend::class.java.classLoader, arrayOf(ResidentBackend::class.java)) { proxy, method, args ->
        when (method.name) {
            "equals" -> proxy === args?.firstOrNull()
            "hashCode" -> System.identityHashCode(proxy)
            "getConnectionKey" -> "projects-test"
            "formatInstant" -> args!![0]
            "relative" -> "2時間前"
            else -> error("Unexpected ${method.name}")
        }
    } as ResidentBackend

    private fun session(id: String, title: String, project: String?, status: String, notify: String? = null, unread: Boolean = false) = SessionListItemUiState(
        session = SessionUiModel(
            id = id, provider = "claude", providerName = "Claude Code", project = project?.substringAfterLast('/') ?: "mercari-watch",
            title = title, cwd = project, status = status, lastMessage = "最後の発言", canSend = true, model = null, effort = null,
            mode = null, contextUsedTokens = null, contextWindowTokens = null, contextUsedPercent = null, costUsd = null,
            costEstimated = null, archived = false, live = true, unread = unread, projectPath = project, projectNotify = notify,
        ),
        relativeUpdatedAt = "2分前",
    )

    private val bot = ProjectDto("/Users/me/workspace/bot", "bot", NotifyLevel.OFF)
    private val herdr = ProjectDto("/Users/me/workspace/herdr-android-client", "herdr-android-client", NotifyLevel.ALL)
    private val onnoti = ProjectDto("/Users/me/workspace/onnoti-tracker", "onnoti-tracker", NotifyLevel.ATTENTION)

    private fun groups() = groupByProject(
        listOf(bot, herdr, onnoti),
        listOf(
            session("s1", "arbscan 採点の再実行", bot.path, Status.COMPLETED, NotifyLevel.OFF, unread = true),
            session("s2", "arbscan 手動投入 Fable", bot.path, Status.RUNNING, NotifyLevel.OFF),
            session("s3", "プロジェクト機能の設計", herdr.path, Status.RUNNING),
            session("s4", "メルカリ監視の調整", null, Status.IDLE),
        ),
        listOf(
            ResidentTaskDto("t1", "arbscan 採点役", provider = "claude", state = TaskState.NEEDS_ATTENTION, unread = 94, projectPath = bot.path, projectNotify = NotifyLevel.OFF),
            ResidentTaskDto("t2", "TikTok分析", provider = "claude", state = TaskState.WAITING_USER, unread = 3, projectPath = onnoti.path, projectNotify = NotifyLevel.ATTENTION),
        ),
    )

    private fun screenshot(name: String) {
        val dir = System.getenv("HERDR_PROJECT_UI_SHOTS")?.let(::File) ?: return
        dir.mkdirs()
        ImageIO.write(rule.onRoot().captureToImage().toAwtImage(), "png", File(dir, "$name.png"))
    }

    @Test
    fun プロジェクトの一覧に件数と通知の段階を出しタップで開く() {
        var opened: String? = null
        rule.setContent {
            MaterialTheme {
                Box(Modifier.size(390.dp, 780.dp)) {
                    ProjectListScreen(ProjectsUiState(groups = groups()), onOpen = { opened = it }, onRefresh = {})
                }
            }
        }
        // bot: a running session and a task that needs attention; onnoti: a question.
        rule.onNodeWithContentDescription("プロジェクト bot").assert(hasText("実行中 1")).assert(hasText("要対応 1")).assert(hasText("常駐 1"))
        rule.onNodeWithContentDescription("プロジェクト onnoti-tracker").assert(hasText("要対応 1"))
        rule.onNodeWithContentDescription("プロジェクト その他").assert(hasText("セッション 1"))
        rule.onNodeWithContentDescription("通知: オフ").assertExists()
        rule.onNodeWithContentDescription("通知: 要対応のみ").assertExists()
        screenshot("projects-list")
        rule.onNodeWithContentDescription("プロジェクト bot").performClick()
        assertEquals(bot.path, opened)
    }

    @Test
    fun プロジェクトを開くと通知を切り替えセッションと常駐を見られる() {
        var group by mutableStateOf(groups().first())
        val levels = mutableListOf<String>()
        var newTaskIn: String? = null
        rule.setContent {
            MaterialTheme {
                Box(Modifier.size(390.dp, 780.dp)) {
                    ProjectDetailScreen(
                        group = group,
                        residentBackend = backend,
                        notifyBusy = false,
                        error = null,
                        onBack = {},
                        onNotify = { level ->
                            levels += level
                            group = group.copy(project = group.project!!.copy(notify = level))
                        },
                        onOpenSession = {},
                        onOpenTask = {},
                        onNewSession = {},
                        onNewTask = { newTaskIn = it },
                    )
                }
            }
        }
        rule.onNodeWithText("arbscan 採点の再実行").assertExists()
        rule.onNodeWithText("通知しません。一覧と未読の印はそのまま残ります。").assertExists()
        screenshot("project-sessions")
        rule.onNodeWithText("要対応のみ").performClick()
        assertEquals(listOf(NotifyLevel.ATTENTION), levels)
        rule.onNodeWithText("質問・承認・失敗だけ通知します。", substring = true).assertExists()
        rule.onNodeWithText("常駐 1").performClick()
        rule.onNodeWithText("arbscan 採点役").assertExists()
        screenshot("project-tasks")
        rule.onNode(hasText("ここで常駐を作る"), useUnmergedTree = true).performClick()
        assertEquals(bot.path, newTaskIn)
    }

    @Test
    fun その他には通知の設定と新規作成を出さない() {
        rule.setContent {
            MaterialTheme {
                Box(Modifier.size(390.dp, 780.dp)) {
                    ProjectDetailScreen(
                        group = groups().last(),
                        residentBackend = backend,
                        notifyBusy = false,
                        error = null,
                        onBack = {}, onNotify = {}, onOpenSession = {}, onOpenTask = {}, onNewSession = {}, onNewTask = {},
                    )
                }
            }
        }
        rule.onNodeWithText("メルカリ監視の調整").assertExists()
        rule.onNodeWithText("要対応のみ").assertDoesNotExist()
        rule.onNode(hasText("ここで新規セッション"), useUnmergedTree = true).assertDoesNotExist()
    }
}
