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

    val releaseKeystore = providers.environmentVariable("ANDROID_KEYSTORE_PATH").orNull
    if (releaseKeystore != null) {
        signingConfigs.create("stableRelease") {
            storeFile = file(releaseKeystore)
            storePassword = providers.environmentVariable("ANDROID_KEYSTORE_PASSWORD").get()
            keyAlias = providers.environmentVariable("ANDROID_KEY_ALIAS").get()
            keyPassword = providers.environmentVariable("ANDROID_KEY_PASSWORD").get()
        }
    }

    buildTypes {
        create("deviceTest") {
            initWith(getByName("debug"))
            applicationIdSuffix = ".devicetest"
            matchingFallbacks += "debug"
        }
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            // Local builds retain the existing installable debug-key behavior.
            // Release CI supplies a persistent key, never a fresh runner's key.
            signingConfig = signingConfigs.getByName(if (releaseKeystore != null) "stableRelease" else "debug")
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }
    // Keep the existing debug JVM/CI tasks; opt in only for isolated device runs.
    if (providers.gradleProperty("deviceTests").orNull == "true") testBuildType = "deviceTest"

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildFeatures {
        compose = true
        buildConfig = true
    }

    testOptions {
        unitTests.isIncludeAndroidResources = true
    }
    sourceSets.getByName("test").resources.srcDir("schemas")
    sourceSets.getByName("deviceTest").java.srcDir("src/debug/java")
    // These tests reference the isolated variant's test host, never the normal APK.
    if (providers.gradleProperty("deviceTests").orNull != "true") {
        sourceSets.getByName("androidTest").java.setSrcDirs(emptyList<String>())
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
    implementation(libs.play.services.code.scanner)
    implementation(libs.androidx.fragment)

    testImplementation(libs.junit)
    testImplementation(libs.robolectric)
    testImplementation(libs.okhttp.mockwebserver)

    androidTestImplementation(libs.androidx.test.junit)
    androidTestImplementation(libs.androidx.test.runner)
    androidTestImplementation(libs.androidx.test.rules)
    // Compose's transitive 3.5.0 uses an InputManager API removed on API 37.
    androidTestImplementation(libs.androidx.test.espresso)
    androidTestImplementation(platform(libs.compose.bom))
    androidTestImplementation(libs.compose.ui.test.junit4)
    androidTestImplementation(libs.okhttp.mockwebserver)
    add("deviceTestImplementation", libs.compose.ui.test.manifest)
}
