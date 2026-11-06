package org.supacode.protomobile.shared

import platform.Foundation.NSLog

internal actual fun platformLog(level: LogLevel, tag: String, message: String) {
    // Kotlin strings are not safe as C varargs, so pass one escaped format string.
    NSLog("${level.name.uppercase()} [$tag] $message".replace("%", "%%"))
}
