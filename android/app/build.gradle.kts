import java.util.Properties

plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.android)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
    alias(libs.plugins.ksp)
}

val releaseProperties = Properties().apply {
    val file = rootProject.projectDir.parentFile.resolve("version.properties")
    require(file.isFile) { "Missing release version source: $file" }
    file.inputStream().use(::load)
}
val herdrVersion = providers.gradleProperty("herdr.version").orNull
    ?: releaseProperties.getProperty("version")
    ?: error("Missing 'version' in version.properties")
val herdrBuild = providers.gradleProperty("herdr.build").orNull
    ?: releaseProperties.getProperty("build")
    ?: error("Missing 'build' in version.properties")

// Shared APKs receive Firebase client settings at pairing time. Only personal
// builds explicitly opting in may embed local google-services.json.
if (providers.gradleProperty("bundleFirebase").orNull == "true" && file("google-services.json").exists()) {
    apply(plugin = libs.plugins.google.services.get().pluginId)
}

android {
    namespace = "com.tohutohu.herdrcompanion"
    compileSdk = 37

    defaultConfig {
        applicationId = "com.tohutohu.herdrcompanion"
        minSdk = 29
        targetSdk = 36
        versionCode = herdrBuild.toIntOrNull()?.takeIf { it > 0 }
            ?: error("Herdr build must be a positive integer, got '$herdrBuild'")
        versionName = herdrVersion
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            // No release keystore yet; the debug key makes the APK installable
            // (and lets it update an installed debug build).
            signingConfig = signingConfigs.getByName("debug")
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildFeatures {
        compose = true
        buildConfig = true
    }
}

kotlin {
    jvmToolchain(17)
}

ksp {
    arg("room.schemaLocation", "$projectDir/schemas")
}

dependencies {
    implementation(project(":shared"))
    implementation(project(":gatewayClient"))

    implementation(libs.androidx.media3.exoplayer)
    implementation(libs.androidx.media3.ui)
    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.lifecycle.process)
    implementation(libs.androidx.navigation3.runtime)
    implementation(libs.androidx.navigation3.ui)
    implementation(libs.androidx.lifecycle.viewmodel.navigation3)
    implementation(libs.androidx.navigationevent.compose)
    implementation(libs.androidx.datastore.preferences)
    implementation(libs.androidx.work.runtime.ktx)

    implementation(libs.androidx.room.runtime)
    implementation(libs.androidx.room.ktx)
    ksp(libs.androidx.room.compiler)

    implementation(platform(libs.compose.bom))
    implementation(libs.compose.ui)
    implementation(libs.compose.foundation)
    implementation(libs.compose.material3)
    implementation(libs.compose.material.icons.extended)
    implementation(libs.compose.ui.tooling.preview)
    debugImplementation(libs.compose.ui.tooling)

    implementation(libs.okhttp)
    implementation(libs.coil.compose)
    implementation(libs.coil.network.okhttp)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.kotlinx.coroutines.android)
    implementation(libs.kotlinx.coroutines.play.services)

    implementation(platform(libs.firebase.bom))
    implementation(libs.firebase.messaging)
    implementation("com.google.android.gms:play-services-code-scanner:16.1.0")
    implementation(libs.androidx.fragment)

    testImplementation(libs.junit)
    testImplementation(libs.okhttp.mockwebserver)
}
