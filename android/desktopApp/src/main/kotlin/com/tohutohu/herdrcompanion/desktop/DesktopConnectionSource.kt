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
}
