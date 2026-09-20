package com.tohutohu.herdrcompanion.ui.sessions

import com.tohutohu.herdrcompanion.model.SessionUiModel

/** Minimal identity used by pure session actions and menus. */
data class SessionRef(val id: String, val live: Boolean, val archived: Boolean)

fun SessionUiModel.toSessionRef(): SessionRef = SessionRef(id, live, archived)
