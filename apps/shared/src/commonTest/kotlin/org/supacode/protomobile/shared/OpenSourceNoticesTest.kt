package org.supacode.protomobile.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class OpenSourceNoticesTest {
    @Test
    fun everyIdIsListedOnce() {
        assertEquals(NoticeId.entries.toList(), OpenSourceNotices.all.map { it.id })
    }

    @Test
    fun platformsSeeSharedAndOwnComponents() {
        val android = OpenSourceNotices.forPlatform(NoticePlatform.Android).map { it.id }
        val ios = OpenSourceNotices.forPlatform(NoticePlatform.Ios).map { it.id }
        assertTrue(NoticeId.Zxing in android && NoticeId.Zxing !in ios)
        assertTrue(NoticeId.Skie in ios && NoticeId.Skie !in android)
        assertTrue(NoticeId.TerminalUi in android && NoticeId.TerminalUi in ios)
        assertTrue(NoticeId.Gvisor in android && NoticeId.Gvisor in ios)
    }

    @Test
    fun licensedComponentsCarryTheirText() {
        OpenSourceNotices.all.forEach { notice ->
            val hasText = !notice.text.isNullOrBlank()
            assertEquals(notice.license != NoticeLicense.PublicData, hasText, notice.name)
        }
        assertTrue(OpenSourceNotices.all.first { it.id == NoticeId.JetBrainsMono }.text!!.contains("SIL OPEN FONT LICENSE"))
    }
}
