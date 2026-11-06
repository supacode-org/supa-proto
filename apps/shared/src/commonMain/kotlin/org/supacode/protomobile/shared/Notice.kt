package org.supacode.protomobile.shared

enum class NoticeLevel { Info, Error }

/** Why a connection failed; the platform turns it into localized text. */
enum class FailureReason {
    NoServerSelected,
    /** [Notice.detail] is the server name. */
    MissingSecret,
    /** [Notice.detail] is the error reported by the system or the Go core. */
    Error,
}

/**
 * Events important enough to interrupt the user with a toast. Anything not
 * modelled here, such as a single DNS lookup failing, belongs in [AppLog].
 * Each type is registered in the app notice spec. [detail] is not
 * user-facing text: platforms localize by type and insert the detail.
 */
sealed interface Notice {
    val level: NoticeLevel
    val detail: String

    /**
     * The server is reachable, at connect time or after an outage. [detail]
     * is the server name; the engine's address is replaced by [ProxyController].
     */
    data class Connected(override val detail: String) : Notice {
        override val level get() = NoticeLevel.Info
    }

    data class ConnectionFailed(val reason: FailureReason, override val detail: String) : Notice {
        override val level get() = NoticeLevel.Error
    }

    /** The proxy went from working to failing; traffic is blocked meanwhile. */
    data class ProxyUnavailable(override val detail: String) : Notice {
        override val level get() = NoticeLevel.Error
    }

    /** Bootstrap DNS could not resolve an infrastructure host (proxy server or DoH). */
    data class BootstrapFailed(override val detail: String) : Notice {
        override val level get() = NoticeLevel.Error
    }

    /** The DoH server itself is unreachable, so no domain can be resolved locally. */
    data class DohUnavailable(override val detail: String) : Notice {
        override val level get() = NoticeLevel.Error
    }

    companion object {
        /** Maps a Go core notice kind; unknown kinds return null. */
        fun fromEngine(kind: String, detail: String): Notice? = when (kind) {
            "connected" -> Connected(detail)
            "connection_failed" -> ConnectionFailed(FailureReason.Error, detail)
            "proxy_unavailable" -> ProxyUnavailable(detail)
            "bootstrap_failed" -> BootstrapFailed(detail)
            "doh_unavailable" -> DohUnavailable(detail)
            else -> null
        }
    }
}
