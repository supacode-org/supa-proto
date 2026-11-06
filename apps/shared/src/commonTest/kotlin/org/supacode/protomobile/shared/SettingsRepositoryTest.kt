package org.supacode.protomobile.shared

import kotlin.test.Test
import kotlin.test.assertEquals

class SettingsRepositoryTest {
    @Test
    fun keepsDnsOptionsPerMode() {
        val store = MemoryStore()
        val settings = AppSettings(
            routingMode = RoutingMode.GeoIpCn,
            modeOptions = mapOf(
                RoutingMode.Global.wire to ModeOptions(dohUrl = "https://global.example/dns-query"),
                RoutingMode.GeoIpCn.wire to ModeOptions(bootstrap = "223.5.5.5:53"),
            ),
        )
        SettingsRepository(store).update(settings)

        val loaded = SettingsRepository(store).current
        assertEquals(settings, loaded)
        assertEquals(RoutingMode.GeoIpCn.defaultDoH, loaded.effectiveDoH)
        assertEquals("223.5.5.5:53", loaded.effectiveBootstrap)

        val global = loaded.copy(routingMode = RoutingMode.Global)
        assertEquals("https://global.example/dns-query", global.effectiveDoH)
        assertEquals(AppSettings.DEFAULT_BOOTSTRAP, global.effectiveBootstrap)
        assertEquals(ModeOptions(), loaded.options(RoutingMode.Custom))
    }

    @Test
    fun migratesLegacyTopLevelDns() {
        val store = MemoryStore()
        store.putString(
            "settings",
            """{"routingMode":"geoip-cn","customRules":"","dohUrl":"https://doh.example/dns-query","bootstrap":"1.1.1.1:53","killSwitch":false}""",
        )

        val loaded = SettingsRepository(store).current
        assertEquals(RoutingMode.GeoIpCn, loaded.routingMode)
        assertEquals(false, loaded.killSwitch)
        assertEquals(ModeOptions("https://doh.example/dns-query", "1.1.1.1:53"), loaded.options(RoutingMode.GeoIpCn))
        assertEquals(ModeOptions(), loaded.options(RoutingMode.Global))
    }
}
