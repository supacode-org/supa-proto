/// Technical notation that belongs to this app: the same in every language, never translated.
/// Generic notation (prompt, `#`, `//`, `→`, bars and so on) comes from `TerminalGlyph`.
enum Glyph {
    static let command = "supaproto"
    static let sessionFile = "session.json"
    static let qrFile = "server.qr"
    static let linkScheme = "supaproto://"
    static let live = "live"
    static let idle = "idle"
    static let keyState = "state"
    static let keyServer = "server"
    static let keyRouting = "routing"
    static let null = "null"
    static let stateConnected = "connected"
    static let stateConnecting = "connecting"
    static let stateDisconnecting = "disconnecting"
    static let stateDisconnected = "disconnected"
    static let stateFailed = "failed"
    static let tcp = "TCP"
    static let udp = "UDP"
    static let memory = "mem"
    static let footerSeparator = " · "
}
