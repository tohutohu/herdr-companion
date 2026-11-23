package com.tohutohu.herdrcompanion.ui.projects

import com.tohutohu.herdrcompanion.data.api.NotifyLevel
import com.tohutohu.herdrcompanion.data.api.ProjectDto
import com.tohutohu.herdrcompanion.data.api.ResidentTaskDto
import com.tohutohu.herdrcompanion.data.api.Status
import com.tohutohu.herdrcompanion.data.api.TaskState
import com.tohutohu.herdrcompanion.model.SessionUiModel
import com.tohutohu.herdrcompanion.ui.sessions.SessionListItemUiState
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class ProjectGroupsTest {
    private fun session(id: String, project: String?, status: String = Status.IDLE, unread: Boolean = false) = SessionListItemUiState(
        session = SessionUiModel(
            id = id, provider = "claude", providerName = "Claude Code", project = "", title = id, cwd = project, status = status,
            lastMessage = null, canSend = true, model = null, effort = null, mode = null, contextUsedTokens = null,
            contextWindowTokens = null, contextUsedPercent = null, costUsd = null, costEstimated = null, archived = false,
            live = true, unread = unread, projectPath = project,
        ),
        relativeUpdatedAt = "",
    )

    private fun task(id: String, project: String?, state: String = TaskState.IDLE, unread: Int = 0) =
        ResidentTaskDto(id = id, name = id, provider = "claude", projectPath = project, state = state, unread = unread)

    private val bot = ProjectDto("/w/bot", "bot", NotifyLevel.OFF)
    private val klon = ProjectDto("/w/klon", "", NotifyLevel.ALL)

    @Test
    fun `プロジェクトの順にまとめどこにも属さないものは最後のその他に入れる`() {
        val groups = groupByProject(
            listOf(bot, klon),
            listOf(session("s1", "/w/klon"), session("s2", "/w/bot"), session("s3", null), session("s4", "/w/removed")),
            listOf(task("t1", "/w/bot"), task("t2", null)),
        )
        assertEquals(listOf("/w/bot", "/w/klon", OTHER_PROJECT_KEY), groups.map { it.key })
        assertEquals(listOf("bot", "klon", "その他"), groups.map { it.name })
        assertEquals(listOf("s2"), groups[0].sessions.map { it.session.id })
        assertEquals(listOf("t1"), groups[0].tasks!!.map { it.id })
        assertEquals(emptyList(), groups[1].tasks)
        assertEquals(listOf("s3", "s4"), groups[2].sessions.map { it.session.id })
        assertEquals(listOf("t2"), groups[2].tasks!!.map { it.id })
        assertNull(groups[2].project)
    }

    @Test
    fun `何も余らなければその他は出さず常駐が未取得なら件数は空のまま`() {
        val groups = groupByProject(listOf(bot), listOf(session("s1", "/w/bot")), null)
        assertEquals(listOf("/w/bot"), groups.map { it.key })
        assertNull(groups.single().tasks)
    }

    @Test
    fun `実行中と要対応と未読をセッションと常駐から数える`() {
        val group = groupByProject(
            listOf(bot),
            listOf(
                session("a", "/w/bot", Status.RUNNING),
                session("b", "/w/bot", Status.WAITING_APPROVAL, unread = true),
                session("c", "/w/bot", Status.FAILED),
                session("d", "/w/bot", Status.COMPLETED, unread = true),
            ),
            listOf(
                task("t1", "/w/bot", TaskState.QUEUED),
                task("t2", "/w/bot", TaskState.NEEDS_ATTENTION, unread = 3),
                task("t3", "/w/bot", TaskState.IDLE),
            ),
        ).single()
        assertEquals(2, group.running)
        assertEquals(3, group.needsYou)
        assertEquals(3, group.unread)
    }

    @Test
    fun `通知の段階に応じてMacで知らせる遷移を絞る`() {
        assertTrue(projectWantsNotification(null, Status.COMPLETED))
        assertTrue(projectWantsNotification(NotifyLevel.ALL, Status.COMPLETED))
        assertFalse(projectWantsNotification(NotifyLevel.ATTENTION, Status.COMPLETED))
        assertTrue(projectWantsNotification(NotifyLevel.ATTENTION, Status.WAITING_INPUT))
        assertTrue(projectWantsNotification(NotifyLevel.ATTENTION, Status.FAILED))
        assertFalse(projectWantsNotification(NotifyLevel.OFF, Status.WAITING_APPROVAL))
    }
}
