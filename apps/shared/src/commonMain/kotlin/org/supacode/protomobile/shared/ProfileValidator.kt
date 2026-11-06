package org.supacode.protomobile.shared

import kotlin.io.encoding.Base64

/** A form problem; the platform turns it into localized text. */
enum class ProfileProblem {
    NameRequired,
    InvalidServer,
    ClientIdRequired,
    SecretNotBase64Url,
    SecretTooShort,
    UnknownObfsProvider,
    InvalidParticipation,
    UnknownPerformanceMode,
    InvalidCoverRate,
}

/** Mirrors the Go client's configuration checks so errors show up in the form. */
object ProfileValidator {
    private val base64Url = Base64.UrlSafe.withPadding(Base64.PaddingOption.ABSENT)

    /**
     * Returns the problems; empty means valid. Pass [secret] = null when
     * editing a profile whose stored secret is kept unchanged.
     */
    fun validate(profile: ServerProfile, secret: String?): List<ProfileProblem> {
        val problems = mutableListOf<ProfileProblem>()
        if (profile.name.isBlank()) problems += ProfileProblem.NameRequired
        if (splitHostPort(profile.server) == null) problems += ProfileProblem.InvalidServer
        if (profile.clientId.isBlank()) problems += ProfileProblem.ClientIdRequired
        if (secret != null) secretProblem(secret)?.let { problems += it }
        if (profile.obfsProvider !in ObfsProviders.all) problems += ProfileProblem.UnknownObfsProvider
        if (profile.obfsParticipation.isNaN() || profile.obfsParticipation !in 0.0..1.0) {
            problems += ProfileProblem.InvalidParticipation
        }
        if (profile.performanceMode !in PerformanceModes.all) problems += ProfileProblem.UnknownPerformanceMode
        if (CoverTraffic.active(profile) &&
            profile.coverRateKbps !in CoverTraffic.MIN_RATE_KBPS..CoverTraffic.MAX_RATE_KBPS
        ) {
            problems += ProfileProblem.InvalidCoverRate
        }
        return problems
    }

    fun secretProblem(secret: String): ProfileProblem? {
        val bytes = try {
            base64Url.decode(secret.trim())
        } catch (_: IllegalArgumentException) {
            return ProfileProblem.SecretNotBase64Url
        }
        return if (bytes.size < 32) ProfileProblem.SecretTooShort else null
    }
}
