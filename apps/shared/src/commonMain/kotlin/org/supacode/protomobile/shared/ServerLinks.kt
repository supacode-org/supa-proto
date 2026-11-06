package org.supacode.protomobile.shared

/** Why a scanned or opened link cannot be imported; platforms localize it. */
enum class LinkProblem {
    NotServerLink,
    UnsupportedVersion,
    MissingCredentials,
    InvalidServer,
    InvalidParameter,
}

/**
 * A server read from a link. [profile] has an empty id; the caller assigns
 * one when the user saves it.
 */
data class ServerLink(val profile: ServerProfile, val secret: String)

sealed interface LinkResult {
    data class Success(val link: ServerLink) : LinkResult
    data class Failure(val problem: LinkProblem) : LinkResult
}

/**
 * supaproto://<client_id>:<secret>@<host>:<port>[?<params>][#<name>]
 *
 * Params: v (format version), obfs, p (participation), perf, udp (1/0).
 * Unknown params are ignored. See the server link spec for details.
 */
object ServerLinks {
    const val SCHEME = "supaproto"
    const val VERSION = 1

    private const val PREFIX = "$SCHEME://"

    fun parse(text: String): LinkResult {
        var rest = text.trim()
        if (!rest.startsWith(PREFIX, ignoreCase = true)) return LinkResult.Failure(LinkProblem.NotServerLink)
        rest = rest.substring(PREFIX.length)

        var name: String? = null
        rest.indexOf('#').takeIf { it >= 0 }?.let { hash ->
            name = percentDecode(rest.substring(hash + 1)) ?: return LinkResult.Failure(LinkProblem.InvalidParameter)
            rest = rest.substring(0, hash)
        }
        val params = mutableMapOf<String, String>()
        rest.indexOf('?').takeIf { it >= 0 }?.let { question ->
            for (pair in rest.substring(question + 1).split('&')) {
                if (pair.isEmpty()) continue
                val eq = pair.indexOf('=')
                val key = percentDecode(if (eq < 0) pair else pair.substring(0, eq))
                val value = percentDecode(if (eq < 0) "" else pair.substring(eq + 1))
                if (key == null || value == null) return LinkResult.Failure(LinkProblem.InvalidParameter)
                params[key] = value
            }
            rest = rest.substring(0, question)
        }
        rest = rest.trimEnd('/')

        val at = rest.lastIndexOf('@')
        val colon = if (at < 0) -1 else rest.substring(0, at).indexOf(':')
        if (colon < 0) return LinkResult.Failure(LinkProblem.MissingCredentials)
        val clientId = percentDecode(rest.substring(0, colon))
        val secret = percentDecode(rest.substring(colon + 1, at))
        if (clientId.isNullOrBlank() || secret.isNullOrBlank()) {
            return LinkResult.Failure(LinkProblem.MissingCredentials)
        }
        val target = splitHostPort(rest.substring(at + 1)) ?: return LinkResult.Failure(LinkProblem.InvalidServer)

        params["v"]?.let { v ->
            val version = v.toIntOrNull() ?: return LinkResult.Failure(LinkProblem.InvalidParameter)
            if (version > VERSION) return LinkResult.Failure(LinkProblem.UnsupportedVersion)
        }
        val obfs = params["obfs"] ?: ObfsProviders.NONE
        if (obfs !in ObfsProviders.all) return LinkResult.Failure(LinkProblem.InvalidParameter)
        val participation = params["p"]?.let { p ->
            p.toDoubleOrNull()?.takeIf { it in 0.0..1.0 } ?: return LinkResult.Failure(LinkProblem.InvalidParameter)
        } ?: defaultParticipation(obfs)
        val performance = params["perf"] ?: PerformanceModes.INTERACTIVE
        if (performance !in PerformanceModes.all) return LinkResult.Failure(LinkProblem.InvalidParameter)
        val udp = when (params["udp"]) {
            null, "1" -> true
            "0" -> false
            else -> return LinkResult.Failure(LinkProblem.InvalidParameter)
        }

        val server = joinHostPort(target.first, target.second)
        val profile = ServerProfile(
            id = "",
            name = name?.trim()?.ifEmpty { null } ?: target.first,
            server = server,
            clientId = clientId,
            obfsProvider = obfs,
            obfsParticipation = participation,
            performanceMode = performance,
            udpEnabled = udp,
        )
        return LinkResult.Success(ServerLink(profile, secret))
    }

    /** Builds the link for [profile]; only non-default params are written. */
    fun format(profile: ServerProfile, secret: String): String = buildString {
        append(PREFIX)
        append(percentEncode(profile.clientId))
        append(':')
        append(percentEncode(secret))
        append('@')
        val target = splitHostPort(profile.server)
        append(if (target != null) joinHostPort(target.first, target.second) else profile.server)
        val params = buildList {
            if (profile.obfsProvider != ObfsProviders.NONE) add("obfs=${percentEncode(profile.obfsProvider)}")
            if (profile.obfsParticipation != defaultParticipation(profile.obfsProvider)) {
                add("p=${profile.obfsParticipation}")
            }
            if (profile.performanceMode != PerformanceModes.INTERACTIVE) {
                add("perf=${percentEncode(profile.performanceMode)}")
            }
            if (!profile.udpEnabled) add("udp=0")
        }
        if (params.isNotEmpty()) append(params.joinToString("&", prefix = "?"))
        append('#')
        append(percentEncode(profile.name))
    }

    private fun defaultParticipation(obfs: String): Double =
        if (obfs == ObfsProviders.NONE) 0.0 else ObfsProviders.DEFAULT_PARTICIPATION

    private fun joinHostPort(host: String, port: Int): String =
        if (':' in host) "[$host]:$port" else "$host:$port"
}

private const val HEX = "0123456789ABCDEF"

/** Percent-encodes everything except RFC 3986 unreserved characters. */
internal fun percentEncode(value: String): String = buildString {
    for (byte in value.encodeToByteArray()) {
        val b = byte.toInt() and 0xFF
        val c = b.toChar()
        if (c in 'A'..'Z' || c in 'a'..'z' || c in '0'..'9' || c == '-' || c == '.' || c == '_' || c == '~') {
            append(c)
        } else {
            append('%').append(HEX[b shr 4]).append(HEX[b and 0x0F])
        }
    }
}

/** Decodes %XX sequences as UTF-8; returns null for malformed input. */
internal fun percentDecode(value: String): String? {
    val bytes = ArrayList<Byte>(value.length)
    var i = 0
    while (i < value.length) {
        val c = value[i]
        if (c == '%') {
            if (i + 2 >= value.length) return null
            val hi = value[i + 1].digitToIntOrNull(16) ?: return null
            val lo = value[i + 2].digitToIntOrNull(16) ?: return null
            bytes += ((hi shl 4) or lo).toByte()
            i += 3
        } else {
            c.toString().encodeToByteArray().forEach { bytes += it }
            i++
        }
    }
    return try {
        bytes.toByteArray().decodeToString(throwOnInvalidSequence = true)
    } catch (_: CharacterCodingException) {
        null
    }
}
