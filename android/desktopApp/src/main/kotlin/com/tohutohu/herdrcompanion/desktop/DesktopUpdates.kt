package com.tohutohu.herdrcompanion.desktop

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.File
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.Paths
import java.security.MessageDigest
import java.util.concurrent.TimeUnit

/** The release workflow publishes both Mac DMGs to this repository's releases. */
private const val LATEST_RELEASE_API = "https://api.github.com/repos/tohutohu/herdr-companion/releases/latest"
private const val DMG_PREFIX = "Herdr-Companion"
private const val APP_NAME = "Herdr Companion.app"
private const val BUNDLE_ID = "com.tohutohu.herdrcompanion.desktop"
private const val FIRST_CHECK_DELAY_MS = 5_000L
private const val CHECK_INTERVAL_MS = 6 * 60 * 60 * 1_000L

@Serializable
internal data class GitHubRelease(
    @SerialName("tag_name") val tagName: String,
    @SerialName("html_url") val htmlUrl: String,
    val draft: Boolean = false,
    val prerelease: Boolean = false,
    val assets: List<GitHubAsset> = emptyList(),
)

@Serializable
internal data class GitHubAsset(
    val name: String,
    @SerialName("browser_download_url") val downloadUrl: String,
    val size: Long = 0,
    /** GitHub's own `sha256:<hex>` of the uploaded file, when it reports one. */
    val digest: String? = null,
)

/** A newer release whose DMG can be verified before it is installed. */
data class DesktopUpdate(
    val version: String,
    val releaseUrl: String,
    val dmgName: String,
    val dmgUrl: String,
    val dmgSize: Long,
    val sha256: String?,
    val sha256Url: String?,
)

sealed interface DesktopUpdateState {
    data object Idle : DesktopUpdateState
    data object Checking : DesktopUpdateState
    data class UpToDate(val version: String) : DesktopUpdateState
    data class Available(val update: DesktopUpdate) : DesktopUpdateState
    /** [progress] is null while the DMG is verified and copied. */
    data class Installing(val update: DesktopUpdate, val progress: Float?) : DesktopUpdateState
    data class Failed(val message: String, val update: DesktopUpdate?) : DesktopUpdateState
}

/** Numeric `MAJOR[.MINOR][.PATCH]` comparison; a missing part counts as 0. */
internal fun compareVersions(a: String, b: String): Int? {
    val left = versionParts(a) ?: return null
    val right = versionParts(b) ?: return null
    for (index in 0 until maxOf(left.size, right.size)) {
        val diff = left.getOrElse(index) { 0 }.compareTo(right.getOrElse(index) { 0 })
        if (diff != 0) return diff
    }
    return 0
}

private fun versionParts(value: String): List<Int>? {
    val parts = value.removePrefix("v").split('.')
    if (parts.isEmpty() || parts.size > 3) return null
    return parts.map { part -> part.takeIf { it.isNotEmpty() && it.all(Char::isDigit) }?.toIntOrNull() ?: return null }
}

private val SHA256_HEX = Regex("[0-9a-f]{64}")

/**
 * Returns the update the release offers over [currentVersion], or null when
 * it is not newer or its DMG has no checksum to verify it against.
 */
internal fun selectUpdate(release: GitHubRelease, currentVersion: String, dmgPrefix: String): DesktopUpdate? {
    if (release.draft || release.prerelease) return null
    val version = release.tagName.removePrefix("v")
    if ((compareVersions(version, currentVersion) ?: return null) <= 0) return null
    val dmgName = "$dmgPrefix-$version.dmg"
    val dmg = release.assets.firstOrNull { it.name == dmgName } ?: return null
    val digest = dmg.digest?.lowercase()?.removePrefix("sha256:")?.takeIf(SHA256_HEX::matches)
    val checksumFile = release.assets.firstOrNull { it.name == "$dmgName.sha256" }?.downloadUrl
    if (digest == null && checksumFile == null) return null
    return DesktopUpdate(
        version = version,
        releaseUrl = release.htmlUrl,
        dmgName = dmgName,
        dmgUrl = dmg.downloadUrl,
        dmgSize = dmg.size,
        sha256 = digest,
        sha256Url = checksumFile,
    )
}

/** Reads `shasum -a 256` output, accepting only the line for [fileName]. */
internal fun parseSha256File(text: String, fileName: String): String? = text.lineSequence()
    .map { it.trim().split(Regex("\\s+"), limit = 2) }
    .firstOrNull { it.size == 2 && it[1].removePrefix("*") == fileName }
    ?.first()?.lowercase()?.takeIf(SHA256_HEX::matches)

/** The `.app` that contains a jpackage launcher, or null when run outside a bundle. */
internal fun appBundleFor(launcher: String?): Path? {
    var path = launcher?.takeIf { it.isNotBlank() }?.let(Paths::get)?.toAbsolutePath() ?: return null
    while (true) {
        if (path.fileName?.toString()?.endsWith(".app") == true) return path
        path = path.parent ?: return null
    }
}

/** Why the app cannot replace itself where it runs, or null when it can. */
internal fun installBlocker(bundle: Path, parentWritable: Boolean): String? {
    val location = bundle.toString()
    return when {
        "/AppTranslocation/" in location || location.startsWith("/Volumes/") ->
            "Move Herdr Companion to the Applications folder, open it from there, and try again."
        !parentWritable -> "This Mac account cannot write to ${bundle.parent}. Install the update from the release page."
        else -> null
    }
}

/**
 * Waits for the app to quit, swaps in the staged bundle, and opens it. The
 * old bundle is put back when the new one cannot be moved into place.
 */
internal const val SWAP_SCRIPT = """
pid="${'$'}1"; new="${'$'}2"; target="${'$'}3"
stage="${'$'}(dirname "${'$'}new")"
tries=0
while kill -0 "${'$'}pid" 2>/dev/null; do
  tries=${'$'}((tries + 1))
  [ "${'$'}tries" -gt 1200 ] && { echo "app did not quit"; exit 1; }
  sleep 0.1
done
backup="${'$'}stage/previous.app"
rm -rf "${'$'}backup"
if mv "${'$'}target" "${'$'}backup"; then
  if mv "${'$'}new" "${'$'}target"; then
    rm -rf "${'$'}stage"
  else
    echo "could not move the new app into place"
    mv "${'$'}backup" "${'$'}target"
  fi
else
  echo "could not move the old app aside"
fi
open "${'$'}target"
"""

private val releaseJson = Json { ignoreUnknownKeys = true }

/**
 * Checks GitHub Releases for a newer Mac UI and, when asked, installs it:
 * the DMG is checksum-verified, its app checked for this bundle ID, version
 * and signature, then swapped in after the app quits.
 */
class DesktopUpdater(
    private val scope: CoroutineScope,
    private val onQuit: () -> Unit,
    private val bundle: Path? = appBundleFor(System.getProperty("jpackage.app-path")),
    private val http: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(15, TimeUnit.SECONDS)
        .readTimeout(60, TimeUnit.SECONDS)
        .build(),
) {
    val currentVersion: String? = bundle?.let { plistValue(it, "CFBundleShortVersionString") }

    /** Updates install only into a packaged app, never into a Gradle run. */
    val supported: Boolean get() = bundle != null && currentVersion != null

    var state by mutableStateOf<DesktopUpdateState>(DesktopUpdateState.Idle)
        private set

    /** "Later" hides the banner until a check finds a different version. */
    var bannerDismissedVersion by mutableStateOf<String?>(null)
        private set

    private var checkJob: Job? = null

    fun startAutomaticChecks() {
        if (!supported) return
        scope.launch {
            delay(FIRST_CHECK_DELAY_MS)
            while (isActive) {
                if (DesktopPreferences.automaticUpdateChecks) check(userInitiated = false)
                delay(CHECK_INTERVAL_MS)
            }
        }
    }

    fun check(userInitiated: Boolean = true) {
        val current = currentVersion
        if (current == null) {
            if (userInitiated) state = DesktopUpdateState.Failed("Updates are available only in the installed app.", null)
            return
        }
        if (state is DesktopUpdateState.Installing || checkJob?.isActive == true) return
        val previous = state
        if (userInitiated) state = DesktopUpdateState.Checking
        checkJob = scope.launch {
            state = runCatching { withContext(Dispatchers.IO) { latestUpdate(current) } }.fold(
                onSuccess = { update ->
                    if (update == null) DesktopUpdateState.UpToDate(current) else DesktopUpdateState.Available(update)
                },
                // A failed background check keeps whatever the last one found.
                onFailure = { error ->
                    if (userInitiated) DesktopUpdateState.Failed(checkFailure(error), null) else previous
                },
            )
        }
    }

    fun dismissBanner() {
        (state as? DesktopUpdateState.Available)?.let { bannerDismissedVersion = it.update.version }
        (state as? DesktopUpdateState.Failed)?.update?.let { bannerDismissedVersion = it.version }
    }

    fun install(update: DesktopUpdate) {
        val bundle = bundle ?: return
        if (state is DesktopUpdateState.Installing) return
        state = DesktopUpdateState.Installing(update, 0f)
        scope.launch {
            val result = runCatching { withContext(Dispatchers.IO) { stage(update, bundle) } }
            result.onSuccess { staged ->
                val restarted = runCatching {
                    ProcessBuilder(
                        "/bin/sh", "-c", SWAP_SCRIPT, "herdr-update",
                        ProcessHandle.current().pid().toString(), staged.toString(), bundle.toString(),
                    )
                        .redirectInput(ProcessBuilder.Redirect.from(File("/dev/null")))
                        .redirectErrorStream(true)
                        .redirectOutput(staged.parent.resolve("update.log").toFile())
                        .start()
                }
                if (restarted.isSuccess) {
                    onQuit()
                } else {
                    state = DesktopUpdateState.Failed("The update could not restart Herdr Companion.", update)
                }
            }.onFailure { error ->
                state = DesktopUpdateState.Failed(error.message ?: "The update could not be installed.", update)
            }
        }
    }

    private fun latestUpdate(current: String): DesktopUpdate? {
        val request = Request.Builder()
            .url(LATEST_RELEASE_API)
            .header("Accept", "application/vnd.github+json")
            .header("User-Agent", "HerdrCompanion/$current")
            .build()
        val body = http.newCall(request).execute().use { response ->
            // Without any published release the endpoint answers 404.
            if (response.code == 404) return null
            check(response.isSuccessful) { "GitHub answered HTTP ${response.code}." }
            response.body.string()
        }
        return selectUpdate(releaseJson.decodeFromString<GitHubRelease>(body), current, DMG_PREFIX)
    }

    private fun checkFailure(error: Throwable): String =
        "Could not check for updates: ${error.message ?: error::class.simpleName}"

    /** Downloads, verifies and copies the new app next to [bundle]; returns the staged app. */
    private suspend fun stage(update: DesktopUpdate, bundle: Path): Path {
        installBlocker(bundle, Files.isWritable(bundle.parent))?.let { error(it) }
        val work = Files.createTempDirectory("herdr-update-")
        try {
            val dmg = work.resolve(update.dmgName)
            val actual = download(update, dmg)
            val expected = update.sha256 ?: update.sha256Url?.let { url ->
                parseSha256File(fetchText(url), update.dmgName)
            } ?: error("The release has no checksum for ${update.dmgName}.")
            check(actual == expected) { "The downloaded update did not match its checksum." }
            state = DesktopUpdateState.Installing(update, null)

            val mount = Files.createDirectory(work.resolve("mount"))
            run("/usr/bin/hdiutil", "attach", "-nobrowse", "-readonly", "-noautoopen", "-mountpoint", mount.toString(), dmg.toString())
            try {
                val app = mount.resolve(APP_NAME)
                check(Files.isDirectory(app)) { "The update DMG does not contain $APP_NAME." }
                check(plistValue(app, "CFBundleIdentifier") == BUNDLE_ID) { "The update is not Herdr Companion." }
                check(plistValue(app, "CFBundleShortVersionString") == update.version) {
                    "The update DMG does not contain version ${update.version}."
                }
                run("/usr/bin/codesign", "--verify", "--deep", "--strict", app.toString())
                // Once the installed app carries a Developer ID, only the same team may replace it.
                teamIdentifier(bundle)?.let { team ->
                    check(teamIdentifier(app) == team) { "The update is signed by a different developer." }
                }
                val stageDir = bundle.parent.resolve(".${bundle.fileName}.update")
                stageDir.toFile().deleteRecursively()
                Files.createDirectory(stageDir)
                val staged = stageDir.resolve(bundle.fileName)
                run("/usr/bin/ditto", app.toString(), staged.toString())
                return staged
            } finally {
                runCatching { run("/usr/bin/hdiutil", "detach", mount.toString()) }
                    .onFailure { runCatching { run("/usr/bin/hdiutil", "detach", "-force", mount.toString()) } }
            }
        } finally {
            work.toFile().deleteRecursively()
        }
    }

    /** Streams the DMG to [destination] and returns its SHA-256. */
    private fun download(update: DesktopUpdate, destination: Path): String {
        val digest = MessageDigest.getInstance("SHA-256")
        val request = Request.Builder().url(update.dmgUrl).header("User-Agent", "HerdrCompanion/$currentVersion").build()
        http.newCall(request).execute().use { response ->
            check(response.isSuccessful) { "The update download failed with HTTP ${response.code}." }
            val body = response.body
            val total = body.contentLength().takeIf { it > 0 } ?: update.dmgSize
            var received = 0L
            var reported = 0f
            body.byteStream().use { input ->
                Files.newOutputStream(destination).use { output ->
                    val buffer = ByteArray(64 * 1024)
                    while (true) {
                        val count = input.read(buffer)
                        if (count < 0) break
                        output.write(buffer, 0, count)
                        digest.update(buffer, 0, count)
                        received += count
                        val progress = if (total > 0) (received.toFloat() / total).coerceIn(0f, 1f) else 0f
                        if (progress - reported >= 0.01f) {
                            reported = progress
                            state = DesktopUpdateState.Installing(update, progress)
                        }
                    }
                }
            }
        }
        return digest.digest().joinToString("") { "%02x".format(it) }
    }

    private fun fetchText(url: String): String {
        val request = Request.Builder().url(url).header("User-Agent", "HerdrCompanion/$currentVersion").build()
        return http.newCall(request).execute().use { response ->
            check(response.isSuccessful) { "The checksum download failed with HTTP ${response.code}." }
            response.body.string()
        }
    }
}

private fun plistValue(app: Path, key: String): String? = runCatching {
    run("/usr/bin/plutil", "-extract", key, "raw", "-o", "-", app.resolve("Contents/Info.plist").toString())
}.getOrNull()?.trim()?.takeIf { it.isNotEmpty() }

/** The signing team, or null for an ad-hoc or unsigned app. */
private fun teamIdentifier(app: Path): String? = runCatching {
    run("/usr/bin/codesign", "-dv", app.toString())
}.getOrNull()?.lineSequence()
    ?.firstOrNull { it.startsWith("TeamIdentifier=") }
    ?.removePrefix("TeamIdentifier=")
    ?.takeIf { it.isNotBlank() && it != "not set" }

/** Runs a system tool and returns its combined output, failing on a non-zero exit. */
private fun run(vararg command: String): String {
    val process = ProcessBuilder(*command).redirectErrorStream(true).start()
    val output = process.inputStream.bufferedReader().readText()
    check(process.waitFor(5, TimeUnit.MINUTES) && process.exitValue() == 0) {
        "${Paths.get(command.first()).fileName} failed: ${output.trim().take(300)}"
    }
    return output
}
