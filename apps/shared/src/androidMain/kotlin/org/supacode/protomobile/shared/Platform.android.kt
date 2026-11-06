package org.supacode.protomobile.shared

import android.os.Build

actual fun platformName(): String = "Android ${Build.VERSION.RELEASE}"
