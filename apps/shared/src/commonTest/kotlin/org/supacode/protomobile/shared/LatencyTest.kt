package org.supacode.protomobile.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class LatencyTest {
    @Test
    fun gradesByThreshold() {
        assertEquals(LatencyGrade.Good, Latency.grade(PingState.Success(99, "")))
        assertEquals(LatencyGrade.Slow, Latency.grade(PingState.Success(100, "")))
        assertEquals(LatencyGrade.Failed, Latency.grade(PingState.Failed("timeout")))
        assertEquals(LatencyGrade.Testing, Latency.grade(PingState.Testing))
    }

    @Test
    fun barShrinksWithLatency() {
        assertEquals(24, Latency.filledCells(PingState.Success(0, "")))
        assertEquals(20, Latency.filledCells(PingState.Success(38, "")))
        assertEquals(10, Latency.filledCells(PingState.Success(142, "")))
        assertEquals(1, Latency.filledCells(PingState.Success(900, "")))
        assertEquals(0, Latency.filledCells(PingState.Failed("")))
        assertNull(Latency.filledCells(PingState.Testing))
    }

    @Test
    fun summaryCountsOnlyFinishedResults() {
        val profiles = listOf(profile("a:1"), profile("b:1"), profile("c:1"), profile("d:1"))
        assertEquals(ServerSummary(4, null, null), ServerSummary.of(profiles, emptyMap()))
        val pings = mapOf(
            "a:1" to PingState.Success(68, ""),
            "b:1" to PingState.Success(38, ""),
            "c:1" to PingState.Failed("timeout"),
            "d:1" to PingState.Testing,
            "gone:1" to PingState.Failed(""),
        )
        assertEquals(ServerSummary(4, 38, 1), ServerSummary.of(profiles, pings))
    }

    private fun profile(server: String) = ServerProfile(id = server, name = server, server = server, clientId = "c")
}
