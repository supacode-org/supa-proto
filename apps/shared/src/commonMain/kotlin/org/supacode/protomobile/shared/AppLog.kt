package org.supacode.protomobile.shared

enum class LogLevel { Debug, Info, Warn, Error }

object AppLog {
    fun d(tag: String, message: String) = platformLog(LogLevel.Debug, tag, message)
    fun i(tag: String, message: String) = platformLog(LogLevel.Info, tag, message)
    fun w(tag: String, message: String) = platformLog(LogLevel.Warn, tag, message)
    fun e(tag: String, message: String) = platformLog(LogLevel.Error, tag, message)
}

internal expect fun platformLog(level: LogLevel, tag: String, message: String)
