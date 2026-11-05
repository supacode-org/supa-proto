package com.json2proxy.shared

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

private const val COVER_SECRET = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

class CoverTrafficTest {
    private fun profile(obfs: String = "iot-cam", enabled: Boolean = true, rate: Int = 2000) = ServerProfile(
        id = "p1", name = "Home", server = "example.net:8443", clientId = "user",
        obfsProvider = obfs, obfsParticipation = if (obfs == ObfsProviders.NONE) 0.0 else 0.65,
        coverEnabled = enabled, coverRateKbps = rate,
    )

    private fun coverOf(profile: ServerProfile) =
        Json.parseToJsonElement(EngineConfig.build(profile, COVER_SECRET, AppSettings()))
            .jsonObject["client"]!!.jsonObject["cover"]?.jsonObject

    @Test
    fun engineConfigCarriesCoverOnlyWhenActive() {
        val cover = coverOf(profile())!!
        assertTrue(cover["enabled"]!!.jsonPrimitive.boolean)
        assertEquals(2000, cover["rate_kbps"]!!.jsonPrimitive.int)

        assertNull(coverOf(profile(enabled = false)))
        // A provider without a cover profile cannot carry cover traffic.
        assertNull(coverOf(profile(obfs = ObfsProviders.NONE)))
    }

    @Test
    fun rateIsCheckedOnlyWhenCoverIsActive() {
        fun problems(p: ServerProfile) = ProfileValidator.validate(p, COVER_SECRET)
        assertEquals(emptyList(), problems(profile(rate = CoverTraffic.MIN_RATE_KBPS)))
        assertEquals(emptyList(), problems(profile(rate = CoverTraffic.MAX_RATE_KBPS)))
        assertEquals(listOf(ProfileProblem.InvalidCoverRate), problems(profile(rate = 63)))
        assertEquals(listOf(ProfileProblem.InvalidCoverRate), problems(profile(rate = 100_001)))
        assertEquals(emptyList(), problems(profile(enabled = false, rate = -1)))
        assertEquals(emptyList(), problems(profile(obfs = ObfsProviders.NONE, rate = -1)))
    }

    @Test
    fun monthlyEstimateUsesFullTimeDecimalGigabytes() {
        assertEquals("324 GB", CoverTraffic.monthlyEstimate(1000))
        assertEquals("648 GB", CoverTraffic.monthlyEstimate(2000))
        assertEquals("20.7 GB", CoverTraffic.monthlyEstimate(64))
        assertEquals("1.30 TB", CoverTraffic.monthlyEstimate(4000))
        assertEquals("32.4 TB", CoverTraffic.monthlyEstimate(100_000))
    }

    @Test
    fun samplerReportsCoverApartFromRealTraffic() {
        val sampler = TrafficSampler()
        val first = sampler.sample("""{"time":0,"up":10,"down":10,"cover":3,"cover_up":0,"cover_down":0}""")!!
        assertEquals(CoverStats(3, 0, 0), first.cover)
        val second = sampler.sample("""{"time":2000,"up":10,"down":10,"cover":4,"cover_up":400,"cover_down":50000}""")!!
        assertEquals(CoverStats(4, 200, 25000), second.cover)
        assertEquals(0, second.downloadSpeed)

        // A core without cover reports none.
        val plain = TrafficSampler().sample("""{"time":0,"up":0,"down":0}""")!!
        assertFalse(plain.cover.isActive)
    }

    @Test
    fun linksLeaveCoverOut() {
        val link = ServerLinks.format(profile(), COVER_SECRET)
        assertFalse(link.contains("cover"))
        val imported = (ServerLinks.parse(link) as LinkResult.Success).link.profile
        assertFalse(imported.coverEnabled)
        assertEquals(CoverTraffic.DEFAULT_RATE_KBPS, imported.coverRateKbps)
    }
}
