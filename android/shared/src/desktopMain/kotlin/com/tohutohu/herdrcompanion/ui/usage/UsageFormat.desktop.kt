package com.tohutohu.herdrcompanion.ui.usage

import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

private val MONTH_DAY_TIME = DateTimeFormatter.ofPattern("M/d HH:mm", Locale.ROOT)

internal actual fun localMonthDayTime(epochMillis: Long): String =
    MONTH_DAY_TIME.format(Instant.ofEpochMilli(epochMillis).atZone(ZoneId.systemDefault()))
