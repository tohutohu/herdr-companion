package com.tohutohu.herdrcompanion.desktop

import com.tohutohu.herdrcompanion.data.api.Status
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class DesktopNotificationsTest {
    @Test
    fun `only meaningful status transitions create notifications`() {
        assertNull(notificationForStatusTransition("one", "project", null, Status.RUNNING))
        assertNull(notificationForStatusTransition("one", "project", Status.RUNNING, Status.RUNNING))
        assertEquals(
            "Your answer is needed",
            notificationForStatusTransition("one", "project", Status.RUNNING, Status.WAITING_INPUT)?.body,
        )
        assertEquals(
            "Approval is needed",
            notificationForStatusTransition("one", "project", Status.WAITING_INPUT, Status.WAITING_APPROVAL)?.body,
        )
        assertEquals(
            "Agent finished",
            notificationForStatusTransition("one", "project", Status.RUNNING, Status.COMPLETED)?.body,
        )
    }
}
