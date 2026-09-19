package com.tohutohu.herdrmobile.data

import com.tohutohu.herdrmobile.data.api.BlockDto

/** A rendered conversation message; the database and transport stay Android-side. */
data class Message(
    val id: String,
    val role: String,
    val timestamp: Long,
    val blocks: List<BlockDto>,
    val queued: Boolean = false,
)
