package org.supacode.protomobile.shared

import kotlin.math.roundToInt

/** How a ping result is colored on the server list. */
enum class LatencyGrade { Testing, Good, Slow, Failed }

object Latency {
    /** Below this many milliseconds a server counts as good. */
    const val GOOD_BELOW_MS = 100L

    /** Width of the character bar under each server. */
    const val BAR_CELLS = 24

    /** Latency that leaves a single filled cell. */
    private const val BAR_SCALE_MS = 250.0

    fun grade(ping: PingState): LatencyGrade = when (ping) {
        PingState.Testing -> LatencyGrade.Testing
        is PingState.Success -> if (ping.millis < GOOD_BELOW_MS) LatencyGrade.Good else LatencyGrade.Slow
        is PingState.Failed -> LatencyGrade.Failed
    }

    /**
     * Filled cells of the latency bar: 1 to [BAR_CELLS] for a result, 0 for a
     * failure (drawn as an empty bar), null while testing (no bar).
     */
    fun filledCells(ping: PingState): Int? = when (ping) {
        PingState.Testing -> null
        is PingState.Success -> ((1 - ping.millis / BAR_SCALE_MS) * BAR_CELLS).roundToInt().coerceIn(1, BAR_CELLS)
        is PingState.Failed -> 0
    }
}

/** The figures above the server list; null means no ping result yet. */
data class ServerSummary(val total: Int, val bestMillis: Long?, val failed: Int?) {
    companion object {
        fun of(profiles: List<ServerProfile>, pings: Map<String, PingState>): ServerSummary {
            val results = profiles.mapNotNull { pings[it.server] }.filter { it != PingState.Testing }
            val best = results.filterIsInstance<PingState.Success>().minOfOrNull { it.millis }
            val failed = results.count { it is PingState.Failed }
            return ServerSummary(profiles.size, best, if (results.isEmpty()) null else failed)
        }
    }
}
