package com.json2proxy.shared

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** One saved server. The secret is stored separately in [SecretStore]. */
@Serializable
data class ServerProfile(
    val id: String,
    val name: String,
    /** host:port; IPv6 addresses use [addr]:port. */
    val server: String,
    val clientId: String,
    val obfsProvider: String = "none",
    val obfsParticipation: Double = 0.0,
    val performanceMode: String = PerformanceModes.INTERACTIVE,
    val udpEnabled: Boolean = true,
    /** Cover traffic; only takes effect with a provider in [ObfsProviders.withCover]. */
    val coverEnabled: Boolean = false,
    /** Target wire rate of all cover connections together. */
    val coverRateKbps: Int = CoverTraffic.DEFAULT_RATE_KBPS,
)

object ObfsProviders {
    const val NONE = "none"
    val all = listOf(NONE, "iot-cam", "mysql")

    /** Participation the server and client use when a provider is enabled. */
    const val DEFAULT_PARTICIPATION = 0.65

    /** Providers whose descriptor declares a cover profile. */
    val withCover = setOf("iot-cam", "mysql")

    fun supportsCover(provider: String): Boolean = provider in withCover
}

/**
 * Cover traffic: extra sessions that exchange decoy data at a steady rate.
 * The Go core decides how many connections to open; the app only sets the
 * target rate. Never part of a server link.
 */
object CoverTraffic {
    const val DEFAULT_RATE_KBPS = 1000
    /** Bounds the Go client accepts for cover.rate_kbps. */
    const val MIN_RATE_KBPS = 64
    const val MAX_RATE_KBPS = 100_000

    /** Whether [profile] would actually generate cover traffic. */
    fun active(profile: ServerProfile): Boolean =
        profile.coverEnabled && ObfsProviders.supportsCover(profile.obfsProvider)

    /**
     * Decimal gigabytes over 30 days online around the clock at [rateKbps]:
     * kbps × 1000 / 8 × 2,592,000 s / 1e9.
     */
    fun monthlyGigabytes(rateKbps: Int): Double = rateKbps * 0.324

    /** [monthlyGigabytes] as "324 GB", or in TB from 1000 GB. */
    fun monthlyEstimate(rateKbps: Int): String {
        val gigabytes = monthlyGigabytes(rateKbps)
        return if (gigabytes < 999.5) "${TrafficFormat.decimal(gigabytes)} GB"
        else "${TrafficFormat.decimal(gigabytes / 1000)} TB"
    }
}

object PerformanceModes {
    const val INTERACTIVE = "interactive"
    const val THROUGHPUT = "throughput"
    val all = listOf(INTERACTIVE, THROUGHPUT)
}

@Serializable
enum class RoutingMode(val wire: String, val defaultDoH: String) {
    @SerialName("global")
    Global("global", "https://dns.google/dns-query"),

    @SerialName("geoip-cn")
    GeoIpCn("geoip-cn", "https://dns.alidns.com/dns-query"),

    @SerialName("custom")
    Custom("custom", "https://dns.google/dns-query"),
}

/** DNS options kept separately for each routing mode. Empty values mean the defaults. */
@Serializable
data class ModeOptions(
    /** Empty means [RoutingMode.defaultDoH]. */
    val dohUrl: String = "",
    /** Empty means [AppSettings.DEFAULT_BOOTSTRAP]. */
    val bootstrap: String = "",
)

@Serializable
data class AppSettings(
    val routingMode: RoutingMode = RoutingMode.Global,
    /** Rule text used when [routingMode] is [RoutingMode.Custom]. */
    val customRules: String = "",
    /**
     * DNS options per routing mode, keyed by [RoutingMode.wire], so switching
     * modes keeps what was entered for each one.
     */
    val modeOptions: Map<String, ModeOptions> = emptyMap(),
    /** Keep the VPN up and block traffic when the proxy fails. */
    val killSwitch: Boolean = true,
    /** iOS Connect On Demand. Android uses the system always-on setting. */
    val autoConnect: Boolean = false,
    /** iOS includeAllNetworks; off by default because it affects system features. */
    val strictKillSwitch: Boolean = false,
    /** Units for data rates shown in the app. */
    val trafficUnit: TrafficUnit = TrafficUnit.Bytes,
) {
    fun options(mode: RoutingMode): ModeOptions = modeOptions[mode.wire] ?: ModeOptions()

    /** Options of the active routing mode. */
    val activeOptions: ModeOptions get() = options(routingMode)

    val effectiveDoH: String get() = activeOptions.dohUrl.trim().ifEmpty { routingMode.defaultDoH }
    val effectiveBootstrap: String get() = activeOptions.bootstrap.trim().ifEmpty { DEFAULT_BOOTSTRAP }

    companion object {
        const val DEFAULT_BOOTSTRAP = "8.8.8.8:53"
    }
}

/** Splits host:port or [ipv6]:port. Returns null when malformed. */
fun splitHostPort(address: String): Pair<String, Int>? {
    val value = address.trim()
    val host: String
    val portText: String
    if (value.startsWith("[")) {
        val end = value.indexOf("]:")
        if (end < 0) return null
        host = value.substring(1, end)
        portText = value.substring(end + 2)
    } else {
        val colon = value.lastIndexOf(':')
        if (colon <= 0 || value.indexOf(':') != colon) return null
        host = value.substring(0, colon)
        portText = value.substring(colon + 1)
    }
    val port = portText.toIntOrNull() ?: return null
    if (host.isEmpty() || port !in 1..65535) return null
    return host to port
}

/**
 * Turns the full-width forms that CJK input methods produce (ＡＢＣ１２３,
 * "。", full-width space) into ASCII and trims. Use it for addresses, IDs
 * and secrets, never for display names.
 */
fun normalizeAscii(input: String): String = buildString(input.length) {
    for (c in input) {
        append(
            when (c) {
                in '\uFF01'..'\uFF5E' -> c - 0xFEE0
                '\u3002' -> '.'
                '\u3000' -> ' '
                else -> c
            },
        )
    }
}.trim()
