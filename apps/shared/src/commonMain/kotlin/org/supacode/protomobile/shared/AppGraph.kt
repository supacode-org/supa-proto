package org.supacode.protomobile.shared

/** Wires the shared objects from the platform services. One per process. */
class AppGraph(
    store: KeyValueStore,
    secrets: SecretStore,
    core: CoreBridge,
    backend: VpnBackend,
    /** Go heap limit for the engine process; 0 keeps the runtime default. */
    memoryLimitMb: Int,
    /** File where the engine keeps the traffic totals across runs; empty keeps none. */
    totalsFile: String,
) {
    val profiles = ProfileRepository(store, secrets)
    val settings = SettingsRepository(store)
    val controller = ProxyController(profiles, settings, backend, core, memoryLimitMb, totalsFile)
}
