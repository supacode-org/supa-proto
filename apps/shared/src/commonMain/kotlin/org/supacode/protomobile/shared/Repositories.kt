package org.supacode.protomobile.shared

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.decodeFromJsonElement
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlin.uuid.Uuid

private val storeJson = Json { ignoreUnknownKeys = true; encodeDefaults = true }

/** Saved servers and the selected one. Secrets live in [SecretStore]. */
class ProfileRepository(private val store: KeyValueStore, private val secrets: SecretStore) {
    private val _profiles = MutableStateFlow(load())
    val profiles: StateFlow<List<ServerProfile>> = _profiles.asStateFlow()

    private val _selectedId = MutableStateFlow(store.getString(KEY_SELECTED))
    val selectedId: StateFlow<String?> = _selectedId.asStateFlow()

    val selected: ServerProfile?
        get() = _profiles.value.firstOrNull { it.id == _selectedId.value }

    fun generateId(): String = Uuid.random().toString()

    fun secret(id: String): String? = secrets.getSecret(id)

    /**
     * Adds or replaces [profile]. A null [secret] keeps the stored one.
     * Returns false, saving nothing, when the secret cannot be stored.
     */
    fun save(profile: ServerProfile, secret: String?): Boolean {
        if (secret != null && !secrets.putSecret(profile.id, secret.trim())) {
            AppLog.e(TAG, "Secret for ${profile.id} could not be stored")
            return false
        }
        val list = _profiles.value.toMutableList()
        val index = list.indexOfFirst { it.id == profile.id }
        if (index >= 0) list[index] = profile else list += profile
        persist(list)
        if (selected == null) select(profile.id)
        return true
    }

    fun delete(id: String) {
        secrets.removeSecret(id)
        persist(_profiles.value.filterNot { it.id == id })
        if (_selectedId.value == id) {
            val next = _profiles.value.firstOrNull()?.id
            if (next != null) select(next) else clearSelection()
        }
    }

    fun select(id: String) {
        if (_profiles.value.none { it.id == id }) return
        store.putString(KEY_SELECTED, id)
        _selectedId.value = id
    }

    private fun clearSelection() {
        store.remove(KEY_SELECTED)
        _selectedId.value = null
    }

    private fun persist(list: List<ServerProfile>) {
        store.putString(KEY_PROFILES, storeJson.encodeToString(list))
        _profiles.value = list
    }

    private fun load(): List<ServerProfile> {
        val raw = store.getString(KEY_PROFILES) ?: return emptyList()
        return try {
            storeJson.decodeFromString(raw)
        } catch (e: Exception) {
            AppLog.e(TAG, "Stored profiles unreadable, starting empty: ${e.message}")
            emptyList()
        }
    }

    private companion object {
        const val TAG = "ProfileRepository"
        const val KEY_PROFILES = "profiles"
        const val KEY_SELECTED = "selected_profile"
    }
}

class SettingsRepository(private val store: KeyValueStore) {
    private val _settings = MutableStateFlow(load())
    val settings: StateFlow<AppSettings> = _settings.asStateFlow()

    val current: AppSettings get() = _settings.value

    fun update(settings: AppSettings) {
        store.putString(KEY_SETTINGS, storeJson.encodeToString(settings))
        _settings.value = settings
    }

    private fun load(): AppSettings {
        val raw = store.getString(KEY_SETTINGS) ?: return AppSettings()
        return try {
            migrateLegacyDns(storeJson.parseToJsonElement(raw).jsonObject)
        } catch (e: Exception) {
            AppLog.e("SettingsRepository", "Stored settings unreadable, using defaults: ${e.message}")
            AppSettings()
        }
    }

    /**
     * Settings saved before DNS options were per mode had top-level "dohUrl" and
     * "bootstrap"; they become the options of the mode that was active then.
     */
    private fun migrateLegacyDns(stored: JsonObject): AppSettings {
        val settings = storeJson.decodeFromJsonElement<AppSettings>(stored)
        if ("modeOptions" in stored) return settings
        val legacy = ModeOptions(
            dohUrl = stored["dohUrl"]?.jsonPrimitive?.contentOrNull.orEmpty(),
            bootstrap = stored["bootstrap"]?.jsonPrimitive?.contentOrNull.orEmpty(),
        )
        if (legacy == ModeOptions()) return settings
        return settings.copy(modeOptions = mapOf(settings.routingMode.wire to legacy))
    }

    private companion object {
        const val KEY_SETTINGS = "settings"
    }
}
