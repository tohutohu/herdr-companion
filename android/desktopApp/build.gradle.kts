import java.util.Properties
import org.jetbrains.compose.desktop.application.dsl.TargetFormat

plugins {
    alias(libs.plugins.kotlin.jvm)
    alias(libs.plugins.compose.multiplatform)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
}

val releaseProperties = Properties().apply {
    val file = rootProject.projectDir.parentFile.resolve("version.properties")
    require(file.isFile) { "Missing release version source: $file" }
    file.inputStream().use(::load)
}

fun releaseValue(name: String): String = providers.gradleProperty("herdr.$name").orNull
    ?: releaseProperties.getProperty(name)
    ?: error("Missing '$name' in version.properties")

val herdrVersion = releaseValue("version")
val herdrBuild = releaseValue("build")
require(Regex("\\d+(?:\\.\\d+){0,2}").matches(herdrVersion)) {
    "Herdr version must be MAJOR[.MINOR][.PATCH], got '$herdrVersion'"
}
require(herdrBuild.toLongOrNull()?.let { it > 0 } == true) {
    "Herdr build must be a positive integer, got '$herdrBuild'"
}

version = herdrVersion

kotlin {
    jvmToolchain(17)
}

dependencies {
    implementation(project(":shared"))
    implementation(project(":gatewayClient"))
    implementation(compose.desktop.currentOs)
    implementation(compose.material3)
    implementation(compose.materialIconsExtended)
    implementation(libs.okhttp)
    implementation(libs.coil.network.okhttp)
    implementation(libs.kotlinx.coroutines.core)
    implementation(libs.kotlinx.serialization.json)

    testImplementation(kotlin("test"))
}

compose.desktop {
    application {
        mainClass = "com.tohutohu.herdrcompanion.desktop.MainKt"

        nativeDistributions {
            targetFormats(TargetFormat.Dmg)
            modules("java.instrument", "java.prefs", "jdk.unsupported")
            packageName = "Herdr"
            packageVersion = herdrVersion
            description = "Desktop client for Herdr coding-agent sessions"
            vendor = "Tohuto H.U."
            copyright = "Copyright © 2026 Tohuto H.U."

            macOS {
                packageName = "Herdr"
                packageVersion = herdrVersion
                packageBuildVersion = herdrBuild
                bundleID = "com.tohutohu.herdrmobile.desktop"
                minimumSystemVersion = "13.0"
                iconFile.set(rootProject.projectDir.parentFile.resolve("macos/assets/Herdr.icns"))

                val signingIdentity = providers.gradleProperty("compose.desktop.mac.signing.identity")
                    .orElse(providers.environmentVariable("MACOS_SIGNING_IDENTITY"))
                if (signingIdentity.isPresent) {
                    signing {
                        sign.set(true)
                        identity.set(signingIdentity)
                        val keychainPath = providers.gradleProperty("compose.desktop.mac.signing.keychain")
                            .orElse(providers.environmentVariable("MACOS_SIGNING_KEYCHAIN"))
                        if (keychainPath.isPresent) {
                            keychain.set(keychainPath)
                        }
                    }
                }

                notarization {
                    appleID.set(providers.gradleProperty("compose.desktop.mac.notarization.appleID")
                        .orElse(providers.environmentVariable("APPLE_ID")))
                    password.set(providers.gradleProperty("compose.desktop.mac.notarization.password")
                        .orElse(providers.environmentVariable("APPLE_APP_SPECIFIC_PASSWORD")))
                    teamID.set(providers.gradleProperty("compose.desktop.mac.notarization.teamID")
                        .orElse(providers.environmentVariable("APPLE_TEAM_ID")))
                }
            }
        }

        buildTypes {
            release {
                proguard {
                    configurationFiles.from(project.file("proguard-rules.pro"))
                }
            }
        }
    }
}

// The Compose plugin derives ProGuard output filenames from the project
// version but does not include that value in the task's up-to-date inputs.
// Make a version bump invalidate the optimized jar before jpackage validates
// its launcher input.
tasks.matching { it.name == "proguardReleaseJars" }.configureEach {
    inputs.property("herdrVersion", herdrVersion)
}
