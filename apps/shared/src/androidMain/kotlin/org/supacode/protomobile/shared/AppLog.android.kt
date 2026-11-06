package org.supacode.protomobile.shared

import android.util.Log

internal actual fun platformLog(level: LogLevel, tag: String, message: String) {
    val priority = when (level) {
        LogLevel.Debug -> Log.DEBUG
        LogLevel.Info -> Log.INFO
        LogLevel.Warn -> Log.WARN
        LogLevel.Error -> Log.ERROR
    }
    Log.println(priority, tag, message)
}
