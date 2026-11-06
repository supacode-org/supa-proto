package org.supacode.protomobile.shared

import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertNull
import kotlin.test.assertTrue

private const val SECRET = "QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI" // 32 bytes

class MemoryStore : KeyValueStore, SecretStore {
    val values = mutableMapOf<String, String>()
    val secrets = mutableMapOf<String, String>()
    override fun getString(key: String) = values[key]
    override fun putString(key: String, value: String) { values[key] = value }
    override fun remove(key: String) { values.remove(key) }
    override fun getSecret(id: String) = secrets[id]
    var failSecrets = false
    override fun putSecret(id: String, secret: String): Boolean {
        if (failSecrets) return false
        secrets[id] = secret
        return true
    }
    override fun removeSecret(id: String) { secrets.remove(id) }
}

class FakeBackend : VpnBackend {
    val started = mutableListOf<String>()
    var stops = 0
    override fun start(engineConfigJson: String) { started += engineConfigJson }
    override fun stop() { stops++ }
    val restarts = mutableListOf<String>()
    override fun restart(engineConfigJson: String) { restarts += engineConfigJson }
    var stats: String? = null
    override fun requestStats(detail: Boolean, callback: (String?) -> Unit) = callback(stats)
}

class FakeCore(private val result: PingResult) : CoreBridge {
    override fun tcpPing(bootstrap: String, host: String, port: Int, timeoutMillis: Int, attempts: Int) = result
    override fun validateRules(text: String) = if (text.contains("FINAL")) null else "rules must end with a FINAL rule"
}

private fun profile(id: String = "p1") = ServerProfile(
    id = id, name = "Home", server = "example.net:8443", clientId = "user",
    obfsProvider = "iot-cam", obfsParticipation = 0.65,
)

class ProxyControllerTest {
    private val store = MemoryStore()
    private val profiles = ProfileRepository(store, store)
    private val settings = SettingsRepository(store)
    private val backend = FakeBackend()

    private fun controller(scope: TestScope? = null, ping: PingResult = PingResult("", 0, 0, 0, "down")) =
        if (scope == null) ProxyController(profiles, settings, backend, FakeCore(ping))
        else ProxyController(profiles, settings, backend, FakeCore(ping), scope = scope.backgroundScope)

    @Test
    fun connectBuildsEngineConfigFromSelectedProfile() {
        profiles.save(profile(), SECRET)
        settings.update(AppSettings(routingMode = RoutingMode.GeoIpCn))
        val c = controller(null)
        c.connect()

        assertIs<ConnectionState.Connecting>(c.state.value)
        val config = Json.parseToJsonElement(backend.started.single()).jsonObject
        assertEquals("geoip-cn", config["mode"]!!.jsonPrimitive.content)
        val client = config["client"]!!.jsonObject
        assertEquals("example.net:8443", client["server"]!!.jsonPrimitive.content)
        assertEquals(SECRET, client["secret"]!!.jsonPrimitive.content)
        assertEquals("iot-cam", client["obfs"]!!.jsonObject["provider"]!!.jsonPrimitive.content)

        c.onVpnStatus(VpnStatus.Connected)
        assertEquals(ConnectionState.Connected("Home"), c.state.value)
        c.onVpnStatus(VpnStatus.Disconnected)
        assertEquals(ConnectionState.Disconnected, c.state.value)
    }

    @Test
    fun connectedNoticeUsesServerName() = runTest {
        profiles.save(profile(), SECRET)
        val c = controller(null)
        c.connect()
        val notice = async(start = CoroutineStart.UNDISPATCHED) { c.notices.first() }
        c.onEngineNotice("connected", "example.net:8443")
        assertEquals(Notice.Connected("Home"), notice.await())
    }

    @Test
    fun connectWithoutServerFails() = runTest {
        val c = controller(null)
        val notice = async(start = CoroutineStart.UNDISPATCHED) { c.notices.first() }
        c.connect()
        assertEquals(ConnectionState.Failed(FailureReason.NoServerSelected, ""), c.state.value)
        assertEquals(Notice.ConnectionFailed(FailureReason.NoServerSelected, ""), notice.await())
        assertTrue(backend.started.isEmpty())
    }

    // runCurrent, unlike advanceUntilIdle, also runs the controller's background collectors.
    @Test
    fun configChangesRestartTheConnectedVpn() = runTest {
        profiles.save(profile(), SECRET)
        val c = controller(this)
        c.connect()
        c.onVpnStatus(VpnStatus.Connected)
        testScheduler.runCurrent()
        assertTrue(backend.restarts.isEmpty(), "connecting alone must not restart")

        settings.update(settings.current.copy(trafficUnit = TrafficUnit.Bits))
        testScheduler.runCurrent()
        assertTrue(backend.restarts.isEmpty(), "display-only settings must not restart")

        settings.update(settings.current.copy(routingMode = RoutingMode.GeoIpCn))
        testScheduler.runCurrent()
        assertEquals("geoip-cn", modeOf(backend.restarts.single()))
        assertEquals(ConnectionState.Connecting("Home"), c.state.value)

        c.onVpnStatus(VpnStatus.Connected)
        profiles.save(profile().copy(server = "example.org:443"), null)
        testScheduler.runCurrent()
        assertEquals(2, backend.restarts.size)
        assertEquals("example.org:443", serverOf(backend.restarts.last()))

        profiles.delete("p1")
        testScheduler.runCurrent()
        assertEquals(1, backend.stops, "removing the connected server disconnects")
    }

    @Test
    fun changesWhileDisconnectedDoNotRestart() = runTest {
        profiles.save(profile(), SECRET)
        val c = controller(this)
        c.connect()
        c.onVpnStatus(VpnStatus.Connected)
        c.onVpnStatus(VpnStatus.Disconnected)
        settings.update(settings.current.copy(routingMode = RoutingMode.GeoIpCn))
        testScheduler.runCurrent()
        assertTrue(backend.restarts.isEmpty())
    }

    private fun modeOf(config: String) = Json.parseToJsonElement(config).jsonObject["mode"]!!.jsonPrimitive.content

    private fun serverOf(config: String) =
        Json.parseToJsonElement(config).jsonObject["client"]!!.jsonObject["server"]!!.jsonPrimitive.content

    @Test
    fun killSwitchOffStopsVpnWhenProxyFails() {
        profiles.save(profile(), SECRET)
        val c = controller(null)
        c.onEngineNotice("proxy_unavailable", "i/o timeout")
        assertEquals(0, backend.stops, "kill switch on must keep the VPN up")

        settings.update(AppSettings(killSwitch = false))
        c.onEngineNotice("proxy_unavailable", "i/o timeout")
        assertEquals(1, backend.stops)
        c.onEngineNotice("connected", "example.net:8443")
        assertEquals(1, backend.stops)
    }

    @Test
    fun pingReportsMedianOrError() = runTest(StandardTestDispatcher()) {
        profiles.save(profile("a"), SECRET)
        val c = controller(this, PingResult("203.0.113.1:8443", 3, 42, 3, ""))
        c.ping("a")
        testScheduler.advanceUntilIdle()
        val state = c.pings.first { it["example.net:8443"] is PingState.Success }["example.net:8443"]
        assertEquals(PingState.Success(42, "203.0.113.1:8443"), state)
    }

    @Test
    fun repositoryKeepsSelectionAndSecrets() {
        profiles.save(profile("a"), SECRET)
        profiles.save(profile("b").copy(name = "Office"), SECRET)
        assertEquals("a", profiles.selectedId.value, "first saved profile is selected")

        profiles.select("b")
        profiles.save(profile("b").copy(name = "Office 2"), null)
        assertEquals(SECRET, profiles.secret("b"), "null secret keeps the stored one")

        profiles.delete("b")
        assertEquals("a", profiles.selectedId.value)
        assertNull(store.secrets["b"])

        store.failSecrets = true
        assertEquals(false, profiles.save(profile("c"), SECRET), "a secret that cannot be stored fails the save")
        assertEquals(listOf("a"), profiles.profiles.value.map { it.id })
        store.failSecrets = false

        val reloaded = ProfileRepository(store, store)
        assertEquals(listOf("a"), reloaded.profiles.value.map { it.id })
        assertEquals("a", reloaded.selectedId.value)
    }

    @Test
    fun validatorMatchesGoRules() {
        assertTrue(ProfileValidator.validate(profile(), SECRET).isEmpty())
        val problems = ProfileValidator.validate(
            profile().copy(name = " ", server = "example.net", obfsParticipation = 1.5),
            "$SECRET=",
        )
        assertEquals(
            listOf(
                ProfileProblem.NameRequired,
                ProfileProblem.InvalidServer,
                ProfileProblem.SecretNotBase64Url,
                ProfileProblem.InvalidParticipation,
            ),
            problems,
        )
        assertEquals(ProfileProblem.SecretTooShort, ProfileValidator.secretProblem("QkJC"))
        assertEquals("example.net" to 8443, splitHostPort("example.net:8443"))
        assertEquals("2001:db8::1" to 443, splitHostPort("[2001:db8::1]:443"))
        assertNull(splitHostPort("2001:db8::1:443"))
        assertEquals("203.0.113.10:8443", normalizeAscii(" ２０３。0。113.10：8443\u3000"))
        assertEquals("名稱 GG", normalizeAscii("名稱 GG"), "non-ASCII text is kept")
    }

    @Test
    fun engineNoticesMapToToasts() {
        assertEquals(Notice.ProxyUnavailable("x"), Notice.fromEngine("proxy_unavailable", "x"))
        assertEquals(
            Notice.ConnectionFailed(FailureReason.Error, "refused"),
            Notice.fromEngine("connection_failed", "refused"),
        )
        assertEquals(NoticeLevel.Info, Notice.fromEngine("connected", "x")!!.level)
        assertNull(Notice.fromEngine("something_new", "x"))
    }
}
