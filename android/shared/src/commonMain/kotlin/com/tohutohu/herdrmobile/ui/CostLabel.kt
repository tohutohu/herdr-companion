package com.tohutohu.herdrmobile.ui

import java.util.Locale

/**
 * What the session spent, as the agents' own `/cost` shows it: "$2.21".
 * An estimate the gateway priced from token counts is marked with "~", and
 * anything below a cent is "<$0.01" rather than "$0.00". Null when the
 * gateway reported no cost, which is the case until a turn has run.
 */
fun costLabel(usd: Double?, estimated: Boolean?): String? {
    if (usd == null || usd <= 0) return null
    val prefix = if (estimated == true) "~" else ""
    if (usd < 0.01) return "$prefix<$0.01"
    val amount = if (usd < 100) String.format(Locale.ROOT, "%.2f", usd)
    else String.format(Locale.ROOT, "%.0f", usd)
    return "$prefix$$amount"
}
