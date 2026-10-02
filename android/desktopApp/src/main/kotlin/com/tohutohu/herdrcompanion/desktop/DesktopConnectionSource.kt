package com.tohutohu.herdrcompanion.desktop

import java.nio.file.Path
import java.nio.file.Paths

/** Reloadable connection settings so a manager started by the UI can create the config. */
class DesktopConnectionSource(
    private val home: Path = Paths.get(System.getProperty("user.home") ?: "."),
    private val environment: Map<String, String> = System.getenv(),
    private val properties: Map<String, String> = System.getProperties().stringPropertyNames()
        .associateWith { System.getProperty(it).orEmpty() },
) {
    @Volatile
    var current: DesktopGatewayConnection = DesktopConnectionConfig.load(home, environment, properties)
        private set

    fun reload(): DesktopGatewayConnection {
        current = DesktopConnectionConfig.load(home, environment, properties)
        return current
    }

    /** Stores an address and token entered in Settings; they win over the manager's config. */
    fun saveManual(connection: DesktopManualConnection): DesktopGatewayConnection {
        DesktopManualConnectionStore.save(home, connection)
        return reload()
    }

    /** Goes back to the config written by the menu-bar manager. */
    fun clearManual(): DesktopGatewayConnection {
        DesktopManualConnectionStore.clear(home)
        return reload()
    }
}
