package org.supacode.protomobile.shared

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlin.math.max
import kotlin.math.round

/** How data rates are shown; only K, M and G prefixes are used. */
@Serializable
enum class TrafficUnit {
    /** KB/s, MB/s, GB/s with 1024 steps. */
    @SerialName("bytes")
    Bytes,

    /** Kb/s, Mb/s, Gb/s with 1000 steps. */
    @SerialName("bits")
    Bits,
}

/** Traffic of the running engine, with speeds from the previous sample. */
data class TrafficStats(
    val tcpConnections: Int,
    val udpConnections: Int,
    /** Bytes since the engine started. */
    val uploadTotal: Long,
    val downloadTotal: Long,
    /** Bytes per second. */
    val uploadSpeed: Long,
    val downloadSpeed: Long,
    /** Bytes of every engine run so far, kept by the engine across runs. */
    val cumulativeUpload: Long = 0,
    val cumulativeDownload: Long = 0,
    /** Cover traffic, counted apart from the connections and bytes above. */
    val cover: CoverStats = CoverStats.None,
    /** Memory of the process running the engine; only the iOS extension reports it. */
    val memory: MemoryUsage? = null,
    /** Empty unless the sample was taken with detail. */
    val hosts: List<HostTraffic> = emptyList(),
    val dns: List<DnsRecord> = emptyList(),
    val flows: List<FlowRecord> = emptyList(),
)

/** Active cover connections and their payload speeds in bytes per second. */
data class CoverStats(val connections: Int, val uploadSpeed: Long, val downloadSpeed: Long) {
    /** Whether there is anything to show. */
    val isActive: Boolean get() = connections > 0

    companion object {
        val None = CoverStats(0, 0, 0)
    }
}

/**
 * Memory of the iOS packet tunnel extension, which the system stops once it
 * passes its limit. [availableBytes] is how much more it may use; 0 when the
 * limit is unknown (the Simulator has none).
 */
data class MemoryUsage(val usedBytes: Long, val availableBytes: Long) {
    /** The system limit, or null when unknown. */
    val limitBytes: Long? get() = if (availableBytes > 0) usedBytes + availableBytes else null

    val pressure: MemoryPressure
        get() {
            val limit = limitBytes ?: return MemoryPressure.Normal
            val share = usedBytes.toDouble() / limit
            return when {
                share >= CRITICAL_SHARE -> MemoryPressure.Critical
                share >= ELEVATED_SHARE -> MemoryPressure.Elevated
                else -> MemoryPressure.Normal
            }
        }

    private companion object {
        const val ELEVATED_SHARE = 0.70
        const val CRITICAL_SHARE = 0.85
    }
}

enum class MemoryPressure { Normal, Elevated, Critical }

/** Overall speeds of one sample, for the traffic chart. */
data class TrafficPoint(val uploadSpeed: Long, val downloadSpeed: Long)

/** The latest sample and the speeds of up to [HISTORY_SIZE] samples before it, oldest first. */
internal data class TrafficFeed(val latest: TrafficStats?, val history: List<TrafficPoint>) {
    fun next(stats: TrafficStats?): TrafficFeed {
        if (stats == null) return copy(latest = null)
        val point = TrafficPoint(stats.uploadSpeed, stats.downloadSpeed)
        return TrafficFeed(stats, (history + point).takeLast(HISTORY_SIZE))
    }

    companion object {
        /** About a minute at one sample per second. */
        const val HISTORY_SIZE = 60
        val Empty = TrafficFeed(null, emptyList())
    }
}

/** Traffic to one domain, or to an IP address when no domain is known. */
data class HostTraffic(
    val host: String,
    val uploadTotal: Long,
    val downloadTotal: Long,
    val uploadSpeed: Long,
    val downloadSpeed: Long,
    val tcpConnections: Int,
    val udpConnections: Int,
    /** PROXY, DIRECT or REJECT. */
    val action: String,
    val rule: String,
    val lastActiveMillis: Long,
)

/** Latest DoH result for a domain resolved on the device. */
@Serializable
data class DnsRecord(
    val name: String,
    val addrs: List<String> = emptyList(),
    val error: String = "",
    val time: Long = 0,
)

/** Routing decision for one connection or UDP flow. */
@Serializable
data class FlowRecord(
    val time: Long = 0,
    val network: String = "",
    val host: String = "",
    val port: Int = 0,
    val action: String = "",
    val rule: String = "",
    val error: String = "",
)

/** Snapshot JSON produced by the Go core's Stats. */
@Serializable
internal data class EngineSnapshot(
    val time: Long,
    val tcp: Int = 0,
    val udp: Int = 0,
    val up: Long = 0,
    val down: Long = 0,
    @SerialName("total_up") val totalUp: Long = 0,
    @SerialName("total_down") val totalDown: Long = 0,
    val cover: Int = 0,
    @SerialName("cover_up") val coverUp: Long = 0,
    @SerialName("cover_down") val coverDown: Long = 0,
    val hosts: List<EngineHost> = emptyList(),
    val dns: List<DnsRecord> = emptyList(),
    val flows: List<FlowRecord> = emptyList(),
    /** Added by the iOS extension. */
    val memory: EngineMemory? = null,
)

@Serializable
internal data class EngineMemory(val used: Long = 0, val available: Long = 0)

@Serializable
internal data class EngineHost(
    val host: String,
    val up: Long = 0,
    val down: Long = 0,
    val tcp: Int = 0,
    val udp: Int = 0,
    val action: String = "",
    val rule: String = "",
    val last: Long = 0,
)

/**
 * Turns successive engine snapshots into [TrafficStats]: speed is the byte
 * difference over the time difference. The first sample, and any sample
 * whose counters went down (the engine restarted), reports zero speed.
 */
class TrafficSampler {
    private var previous: EngineSnapshot? = null
    private var previousHosts: Map<String, EngineHost> = emptyMap()

    /** Returns null when [json] is empty or unreadable. */
    fun sample(json: String): TrafficStats? {
        if (json.isEmpty()) return null
        val current = try {
            snapshotJson.decodeFromString<EngineSnapshot>(json)
        } catch (e: Exception) {
            AppLog.e("TrafficSampler", "Unreadable stats: ${e.message}")
            return null
        }
        val last = previous
        val elapsed = if (last == null) 0 else current.time - last.time
        val stats = TrafficStats(
            tcpConnections = current.tcp,
            udpConnections = current.udp,
            uploadTotal = current.up,
            downloadTotal = current.down,
            uploadSpeed = speed(last?.up, current.up, elapsed),
            downloadSpeed = speed(last?.down, current.down, elapsed),
            cumulativeUpload = current.totalUp,
            cumulativeDownload = current.totalDown,
            cover = CoverStats(
                connections = current.cover,
                uploadSpeed = speed(last?.coverUp, current.coverUp, elapsed),
                downloadSpeed = speed(last?.coverDown, current.coverDown, elapsed),
            ),
            memory = current.memory?.let { MemoryUsage(it.used, it.available) },
            hosts = current.hosts.map { host ->
                val before = previousHosts[host.host]
                HostTraffic(
                    host = host.host,
                    uploadTotal = host.up,
                    downloadTotal = host.down,
                    uploadSpeed = speed(before?.up, host.up, elapsed),
                    downloadSpeed = speed(before?.down, host.down, elapsed),
                    tcpConnections = host.tcp,
                    udpConnections = host.udp,
                    action = host.action,
                    rule = host.rule,
                    lastActiveMillis = host.last,
                )
            },
            dns = current.dns,
            flows = current.flows,
        )
        previous = current
        // A summary sample has no hosts; keep the last detail for the next one.
        if (current.hosts.isNotEmpty()) previousHosts = current.hosts.associateBy { it.host }
        return stats
    }

    private fun speed(before: Long?, now: Long, elapsedMillis: Long): Long {
        if (before == null || elapsedMillis <= 0 || now < before) return 0
        return (now - before) * 1000 / elapsedMillis
    }

    private companion object {
        val snapshotJson = Json { ignoreUnknownKeys = true }
    }
}

object TrafficFormat {
    private val byteUnits = listOf("KB", "MB", "GB")
    private val bitUnits = listOf("Kb", "Mb", "Gb")

    /**
     * Formats a rate with K, M or G only: below 1 K stays in K and anything
     * past G stays in G. Values under 10 get two decimals, under 100 one.
     */
    fun rate(bytesPerSecond: Long, unit: TrafficUnit): String = "${amount(bytesPerSecond, unit)}/s"

    /** Formats a quantity of data with the same units and rules as [rate], without "/s". */
    fun amount(bytes: Long, unit: TrafficUnit): String {
        val (base, units, amount) = when (unit) {
            TrafficUnit.Bytes -> Triple(1024.0, byteUnits, bytes.toDouble())
            TrafficUnit.Bits -> Triple(1000.0, bitUnits, bytes * 8.0)
        }
        var value = max(amount, 0.0) / base
        var index = 0
        // Step up once the value would round to a full unit, so 1023.7 KB/s reads 1.00 MB/s.
        while (index < units.lastIndex && value >= base - 0.5) {
            value /= base
            index++
        }
        return "${decimal(value)} ${units[index]}"
    }

    internal fun decimal(value: Double): String {
        val decimals = when {
            value < 9.995 -> 2
            value < 99.95 -> 1
            else -> 0
        }
        var factor = 1L
        repeat(decimals) { factor *= 10 }
        val scaled = round(value * factor).toLong()
        if (decimals == 0) return scaled.toString()
        val fraction = (scaled % factor).toString().padStart(decimals, '0')
        return "${scaled / factor}.$fraction"
    }
}
