package org.supacode.protomobile.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs

private const val LINK_SECRET = "QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI"

class ServerLinksTest {
    private fun parsed(text: String): ServerLink {
        val result = ServerLinks.parse(text)
        assertIs<LinkResult.Success>(result, "parse($text) = $result")
        return result.link
    }

    private fun problem(text: String): LinkProblem {
        val result = ServerLinks.parse(text)
        assertIs<LinkResult.Failure>(result, "parse($text) = $result")
        return result.problem
    }

    @Test
    fun parsesFullLink() {
        val link = parsed(
            "SupaProto://local-dev-user:$LINK_SECRET@203.0.113.10:8443" +
                "?obfs=iot-cam&p=0.5&perf=throughput&udp=0&future=x#GG%20Vision",
        )
        assertEquals(
            ServerProfile(
                id = "", name = "GG Vision", server = "203.0.113.10:8443", clientId = "local-dev-user",
                obfsProvider = "iot-cam", obfsParticipation = 0.5, performanceMode = "throughput", udpEnabled = false,
            ),
            link.profile,
        )
        assertEquals(LINK_SECRET, link.secret)
    }

    @Test
    fun appliesDefaults() {
        val plain = parsed("supaproto://u:$LINK_SECRET@example.net:443")
        assertEquals("example.net", plain.profile.name, "name defaults to the host")
        assertEquals(ObfsProviders.NONE, plain.profile.obfsProvider)
        assertEquals(0.0, plain.profile.obfsParticipation)
        assertEquals(PerformanceModes.INTERACTIVE, plain.profile.performanceMode)
        assertEquals(true, plain.profile.udpEnabled)

        val obfs = parsed("supaproto://u:$LINK_SECRET@example.net:443?obfs=mysql")
        assertEquals(ObfsProviders.DEFAULT_PARTICIPATION, obfs.profile.obfsParticipation)
    }

    @Test
    fun roundTripsUnusualValues() {
        val profile = ServerProfile(
            id = "", name = "家裡 / Home #1", server = "[2001:db8::1]:8443", clientId = "alice@example:1",
            obfsProvider = "iot-cam", obfsParticipation = 0.65, performanceMode = "interactive", udpEnabled = true,
        )
        val text = ServerLinks.format(profile, LINK_SECRET)
        assertEquals(
            "supaproto://alice%40example%3A1:$LINK_SECRET@[2001:db8::1]:8443?obfs=iot-cam" +
                "#%E5%AE%B6%E8%A3%A1%20%2F%20Home%20%231",
            text,
            "default participation is omitted",
        )
        assertEquals(ServerLink(profile, LINK_SECRET), parsed(text))
    }

    @Test
    fun rejectsBadLinks() {
        assertEquals(LinkProblem.NotServerLink, problem("https://example.com"))
        assertEquals(LinkProblem.NotServerLink, problem("hello"))
        assertEquals(LinkProblem.UnsupportedVersion, problem("supaproto://u:$LINK_SECRET@h:1?v=2"))
        assertEquals(LinkProblem.MissingCredentials, problem("supaproto://h:1"))
        assertEquals(LinkProblem.MissingCredentials, problem("supaproto://u@h:1"))
        assertEquals(LinkProblem.MissingCredentials, problem("supaproto://:$LINK_SECRET@h:1"))
        assertEquals(LinkProblem.InvalidServer, problem("supaproto://u:$LINK_SECRET@h"))
        assertEquals(LinkProblem.InvalidServer, problem("supaproto://u:$LINK_SECRET@h:70000"))
        assertEquals(LinkProblem.InvalidParameter, problem("supaproto://u:$LINK_SECRET@h:1?obfs=rot13"))
        assertEquals(LinkProblem.InvalidParameter, problem("supaproto://u:$LINK_SECRET@h:1?p=2"))
        assertEquals(LinkProblem.InvalidParameter, problem("supaproto://u:$LINK_SECRET@h:1?udp=yes"))
        assertEquals(LinkProblem.InvalidParameter, problem("supaproto://u:$LINK_SECRET@h:1#%E5%AE"))
        assertEquals(LinkProblem.InvalidParameter, problem("supaproto://u:$LINK_SECRET@h:1#%zz"))
    }
}
