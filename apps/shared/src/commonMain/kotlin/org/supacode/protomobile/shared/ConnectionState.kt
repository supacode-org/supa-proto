package org.supacode.protomobile.shared

sealed interface ConnectionState {
    data object Disconnected : ConnectionState
    data class Connecting(val server: String) : ConnectionState
    data class Connected(val server: String) : ConnectionState
    data object Disconnecting : ConnectionState
    data class Failed(val reason: FailureReason, val detail: String) : ConnectionState
}

sealed interface PingState {
    data object Testing : PingState
    data class Success(val millis: Long, val address: String) : PingState
    /** [detail] is the Go core's error text, or empty when the address is invalid. */
    data class Failed(val detail: String) : PingState
}
