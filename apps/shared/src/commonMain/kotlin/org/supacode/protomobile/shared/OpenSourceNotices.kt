package org.supacode.protomobile.shared

enum class NoticeCategory { Library, Font, Data }

/** Which app ships the component. */
enum class NoticePlatform { All, Android, Ios }

/** The license a component is used under; platforms show [spdx] verbatim, or a localized name when it is null. */
enum class NoticeLicense(val spdx: String?) {
    Apache2("Apache-2.0"),
    Bsd3Clause("BSD-3-Clause"),
    Ofl11("OFL-1.1"),
    Mit("MIT"),

    /** Public statistics published by a registry; there is no license text. */
    PublicData(null),
}

/** Every component listed on the notices page; platforms localize each one's purpose by id. */
enum class NoticeId {
    GoRuntime, GoExtensions, Gvisor, Btree,
    KotlinStdlib, KotlinxCoroutines, KotlinxSerialization, Skie,
    AndroidX, Zxing, TerminalUi,
    JetBrainsMono, ApnicStats,
}

data class OpenSourceNotice(
    val id: NoticeId,
    /** Proper name; not translated. */
    val name: String,
    val copyright: String,
    val url: String,
    val category: NoticeCategory,
    val license: NoticeLicense,
    val platform: NoticePlatform,
    /** Full license text, or null when there is none. */
    val text: String?,
)

/**
 * Third-party components the apps ship or link, with their licenses. Keep in
 * step with go.mod and gradle/libs.versions.toml.
 */
object OpenSourceNotices {
    val all: List<OpenSourceNotice> = listOf(
        notice(
            NoticeId.GoRuntime, "Go", "Copyright 2009 The Go Authors", "https://go.dev",
            NoticeCategory.Library, NoticeLicense.Bsd3Clause, NoticePlatform.All, BSD_3_CLAUSE_GO,
        ),
        notice(
            NoticeId.GoExtensions, "golang.org/x/net, x/sys, x/sync, x/time, x/mobile", "Copyright 2009 The Go Authors",
            "https://cs.opensource.google/go/x", NoticeCategory.Library, NoticeLicense.Bsd3Clause, NoticePlatform.All, BSD_3_CLAUSE_GO,
        ),
        notice(
            NoticeId.Gvisor, "gVisor", "Copyright The gVisor Authors", "https://gvisor.dev",
            NoticeCategory.Library, NoticeLicense.Apache2, NoticePlatform.All, GVISOR_LICENSE,
        ),
        notice(
            NoticeId.Btree, "google/btree", "Copyright Google Inc.", "https://github.com/google/btree",
            NoticeCategory.Library, NoticeLicense.Apache2, NoticePlatform.All, APACHE_2_0,
        ),
        notice(
            NoticeId.KotlinStdlib, "Kotlin", "Copyright JetBrains s.r.o. and Kotlin Programming Language contributors",
            "https://kotlinlang.org", NoticeCategory.Library, NoticeLicense.Apache2, NoticePlatform.All, APACHE_2_0,
        ),
        notice(
            NoticeId.KotlinxCoroutines, "kotlinx.coroutines", "Copyright JetBrains s.r.o. and contributors",
            "https://github.com/Kotlin/kotlinx.coroutines", NoticeCategory.Library, NoticeLicense.Apache2, NoticePlatform.All, APACHE_2_0,
        ),
        notice(
            NoticeId.KotlinxSerialization, "kotlinx.serialization", "Copyright JetBrains s.r.o. and contributors",
            "https://github.com/Kotlin/kotlinx.serialization", NoticeCategory.Library, NoticeLicense.Apache2, NoticePlatform.All, APACHE_2_0,
        ),
        notice(
            NoticeId.Skie, "SKIE", "Copyright Touchlab", "https://github.com/touchlab/SKIE",
            NoticeCategory.Library, NoticeLicense.Apache2, NoticePlatform.Ios, APACHE_2_0,
        ),
        notice(
            NoticeId.AndroidX, "AndroidX (Compose, Activity, Lifecycle, CameraX)", "Copyright The Android Open Source Project",
            "https://developer.android.com/jetpack/androidx", NoticeCategory.Library, NoticeLicense.Apache2, NoticePlatform.Android, APACHE_2_0,
        ),
        notice(
            NoticeId.Zxing, "ZXing", "Copyright ZXing authors", "https://github.com/zxing/zxing",
            NoticeCategory.Library, NoticeLicense.Apache2, NoticePlatform.Android, APACHE_2_0,
        ),
        notice(
            NoticeId.TerminalUi, "Terminal UI", "Copyright (c) 2026 Weirui Kong", "https://github.com/supacode-org/terminal-ui",
            NoticeCategory.Library, NoticeLicense.Mit, NoticePlatform.All, MIT_TERMINAL_UI,
        ),
        notice(
            NoticeId.JetBrainsMono, "JetBrains Mono", "Copyright 2020 The JetBrains Mono Project Authors",
            "https://github.com/JetBrains/JetBrainsMono", NoticeCategory.Font, NoticeLicense.Ofl11, NoticePlatform.All, OFL_1_1,
        ),
        notice(
            NoticeId.ApnicStats, "APNIC delegated statistics", "Asia Pacific Network Information Centre",
            "https://ftp.apnic.net/stats/apnic/", NoticeCategory.Data, NoticeLicense.PublicData, NoticePlatform.All, null,
        ),
    )

    /** Components shipped on one platform, in display order. */
    fun forPlatform(platform: NoticePlatform): List<OpenSourceNotice> =
        all.filter { it.platform == NoticePlatform.All || it.platform == platform }

    private fun notice(
        id: NoticeId,
        name: String,
        copyright: String,
        url: String,
        category: NoticeCategory,
        license: NoticeLicense,
        platform: NoticePlatform,
        text: String?,
    ) = OpenSourceNotice(id, name, copyright, url, category, license, platform, text?.trim('\n'))
}
