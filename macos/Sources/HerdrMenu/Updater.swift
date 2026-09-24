import AppKit
import CryptoKit
import Foundation

/// The release workflow publishes both Mac DMGs to this repository's releases.
private let latestReleaseAPI = URL(string: "https://api.github.com/repos/tohutohu/herdr-companion/releases/latest")!
private let dmgPrefix = "Herdr-Companion-Gateway"
private let appName = "Herdr Companion Gateway.app"
private let bundleID = "com.tohutohu.herdrcompanion.gateway"

struct GitHubRelease: Decodable {
    let tagName: String
    let htmlURL: String
    var draft = false
    var prerelease = false
    var assets: [GitHubAsset] = []

    enum CodingKeys: String, CodingKey {
        case tagName = "tag_name", htmlURL = "html_url", draft, prerelease, assets
    }

    init(tagName: String, htmlURL: String, draft: Bool = false, prerelease: Bool = false, assets: [GitHubAsset] = []) {
        self.tagName = tagName; self.htmlURL = htmlURL; self.draft = draft; self.prerelease = prerelease; self.assets = assets
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        tagName = try c.decode(String.self, forKey: .tagName)
        htmlURL = try c.decode(String.self, forKey: .htmlURL)
        draft = try c.decodeIfPresent(Bool.self, forKey: .draft) ?? false
        prerelease = try c.decodeIfPresent(Bool.self, forKey: .prerelease) ?? false
        assets = try c.decodeIfPresent([GitHubAsset].self, forKey: .assets) ?? []
    }
}

struct GitHubAsset: Decodable {
    let name: String
    let downloadURL: String
    /// GitHub's own `sha256:<hex>` of the uploaded file, when it reports one.
    var digest: String?

    enum CodingKeys: String, CodingKey { case name, downloadURL = "browser_download_url", digest }
}

/// A newer release whose DMG can be verified before it is installed.
struct AppUpdate: Equatable {
    let version: String
    let releaseURL: URL
    let dmgName: String
    let dmgURL: URL
    let sha256: String?
    let sha256URL: URL?
}

enum UpdateState: Equatable {
    case idle, checking
    case upToDate(String)
    case available(AppUpdate)
    case installing(AppUpdate)
    case failed(String, AppUpdate?)
}

/// Numeric `MAJOR[.MINOR][.PATCH]` comparison; a missing part counts as 0.
func compareVersions(_ a: String, _ b: String) -> Int? {
    guard let left = versionParts(a), let right = versionParts(b) else { return nil }
    for i in 0..<max(left.count, right.count) {
        let l = i < left.count ? left[i] : 0, r = i < right.count ? right[i] : 0
        if l != r { return l < r ? -1 : 1 }
    }
    return 0
}

private func versionParts(_ value: String) -> [Int]? {
    let text = value.hasPrefix("v") ? String(value.dropFirst()) : value
    let parts = text.split(separator: ".", omittingEmptySubsequences: false)
    guard (1...3).contains(parts.count) else { return nil }
    var numbers: [Int] = []
    for part in parts {
        guard !part.isEmpty, part.allSatisfy(\.isASCII), part.allSatisfy(\.isNumber), let n = Int(part) else { return nil }
        numbers.append(n)
    }
    return numbers
}

private func sha256Hex(_ value: String?) -> String? {
    guard let value = value?.lowercased(), value.count == 64,
          value.allSatisfy({ $0.isHexDigit && $0.isASCII }) else { return nil }
    return value
}

/// Returns the update the release offers over `current`, or nil when it is
/// not newer or its DMG has no checksum to verify it against.
func selectUpdate(_ release: GitHubRelease, current: String, dmgPrefix: String) -> AppUpdate? {
    guard !release.draft, !release.prerelease else { return nil }
    let version = release.tagName.hasPrefix("v") ? String(release.tagName.dropFirst()) : release.tagName
    guard let order = compareVersions(version, current), order > 0 else { return nil }
    let dmgName = "\(dmgPrefix)-\(version).dmg"
    guard let dmg = release.assets.first(where: { $0.name == dmgName }),
          let dmgURL = URL(string: dmg.downloadURL),
          let releaseURL = URL(string: release.htmlURL) else { return nil }
    let digest = dmg.digest.flatMap { $0.lowercased().hasPrefix("sha256:") ? sha256Hex(String($0.dropFirst(7))) : nil }
    let checksumFile = release.assets.first(where: { $0.name == "\(dmgName).sha256" }).flatMap { URL(string: $0.downloadURL) }
    guard digest != nil || checksumFile != nil else { return nil }
    return AppUpdate(version: version, releaseURL: releaseURL, dmgName: dmgName, dmgURL: dmgURL, sha256: digest, sha256URL: checksumFile)
}

/// Reads `shasum -a 256` output, accepting only the line for `fileName`.
func parseSha256File(_ text: String, fileName: String) -> String? {
    for line in text.split(whereSeparator: \.isNewline) {
        let fields = line.split(maxSplits: 1, whereSeparator: \.isWhitespace)
        guard fields.count == 2 else { continue }
        var name = fields[1].trimmingCharacters(in: .whitespaces)
        if name.hasPrefix("*") { name.removeFirst() }
        if name == fileName { return sha256Hex(String(fields[0])) }
    }
    return nil
}

/// Why the app cannot replace itself where it runs, or nil when it can.
func installBlocker(bundle: URL, parentWritable: Bool) -> String? {
    let path = bundle.path
    if path.contains("/AppTranslocation/") || path.hasPrefix("/Volumes/") {
        return "Herdr Companion GatewayをApplicationsフォルダへ移動し、そこから開いてから再試行してください。"
    }
    if !parentWritable { return "\(bundle.deletingLastPathComponent().path)に書き込めません。リリースページから更新してください。" }
    return nil
}

/// Waits for the app to quit, swaps in the staged bundle, and opens it. The
/// old bundle is put back when the new one cannot be moved into place.
let swapScript = """
pid="$1"; new="$2"; target="$3"
stage="$(dirname "$new")"
tries=0
while kill -0 "$pid" 2>/dev/null; do
  tries=$((tries + 1))
  [ "$tries" -gt 1200 ] && { echo "app did not quit"; exit 1; }
  sleep 0.1
done
backup="$stage/previous.app"
rm -rf "$backup"
if mv "$target" "$backup"; then
  if mv "$new" "$target"; then
    rm -rf "$stage"
  else
    echo "could not move the new app into place"
    mv "$backup" "$target"
  fi
else
  echo "could not move the old app aside"
fi
open "$target"
"""

/// Checks GitHub Releases for a newer Gateway Manager and, when asked,
/// installs it: the DMG is checksum-verified, its app checked for this bundle
/// ID, version and signature, then swapped in after the app quits.
@MainActor final class Updater: ObservableObject {
    @Published private(set) var state: UpdateState = .idle
    @Published var automaticChecks = UserDefaults.standard.object(forKey: "automaticUpdateChecks") as? Bool ?? true {
        didSet { UserDefaults.standard.set(automaticChecks, forKey: "automaticUpdateChecks") }
    }
    let currentVersion = Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String
    private let bundle = Bundle.main.bundleURL
    private var checking = false
    private var timer: Timer?

    /// Updates install only into a packaged app, never into `swift run`.
    var supported: Bool { bundle.pathExtension == "app" && currentVersion != nil }

    var availableUpdate: AppUpdate? {
        switch state {
        case .available(let update), .installing(let update): return update
        case .failed(_, let update): return update
        default: return nil
        }
    }

    func startAutomaticChecks() {
        guard supported, timer == nil else { return }
        Task { @MainActor [weak self] in
            try? await Task.sleep(nanoseconds: 5_000_000_000)
            self?.automaticCheck()
        }
        timer = Timer.scheduledTimer(withTimeInterval: 6 * 60 * 60, repeats: true) { [weak self] _ in
            Task { @MainActor [weak self] in self?.automaticCheck() }
        }
    }

    private func automaticCheck() {
        if automaticChecks { check(userInitiated: false) }
    }

    func check(userInitiated: Bool = true) {
        guard let current = currentVersion, supported else {
            if userInitiated { state = .failed("アップデートはインストールしたアプリでのみ利用できます。", nil) }
            return
        }
        if case .installing = state { return }
        guard !checking else { return }
        checking = true
        let previous = state
        if userInitiated { state = .checking }
        Task { @MainActor [weak self] in
            guard let self else { return }
            defer { self.checking = false }
            do {
                if let update = try await Self.latestUpdate(current: current) {
                    self.state = .available(update)
                } else {
                    self.state = .upToDate(current)
                }
            } catch {
                // A failed background check keeps whatever the last one found.
                self.state = userInitiated ? .failed("アップデートを確認できませんでした: \(error.localizedDescription)", nil) : previous
            }
        }
    }

    /// `beforeQuit` stops work that must end before the new app starts.
    func install(_ update: AppUpdate, beforeQuit: @escaping @MainActor () async -> Void) {
        if case .installing = state { return }
        state = .installing(update)
        let bundle = bundle
        Task { @MainActor [weak self] in
            do {
                let staged = try await Self.stage(update, bundle: bundle)
                let process = Process()
                process.executableURL = URL(fileURLWithPath: "/bin/sh")
                process.arguments = ["-c", swapScript, "herdr-update", String(ProcessInfo.processInfo.processIdentifier), staged.path, bundle.path]
                process.standardInput = FileHandle.nullDevice
                let log = staged.deletingLastPathComponent().appendingPathComponent("update.log")
                FileManager.default.createFile(atPath: log.path, contents: nil)
                let handle = try FileHandle(forWritingTo: log)
                process.standardOutput = handle; process.standardError = handle
                try process.run()
                await beforeQuit()
                NSApp.terminate(nil)
            } catch {
                self?.state = .failed(error.localizedDescription, update)
            }
        }
    }

    private nonisolated static func latestUpdate(current: String) async throws -> AppUpdate? {
        var request = URLRequest(url: latestReleaseAPI)
        request.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept")
        request.setValue("HerdrCompanionGateway/\(current)", forHTTPHeaderField: "User-Agent")
        request.timeoutInterval = 15
        let (data, response) = try await URLSession.shared.data(for: request)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        // Without any published release the endpoint answers 404.
        if status == 404 { return nil }
        guard (200..<300).contains(status) else { throw SetupError(message: "GitHubがHTTP \(status)を返しました。") }
        return selectUpdate(try JSONDecoder().decode(GitHubRelease.self, from: data), current: current, dmgPrefix: dmgPrefix)
    }

    /// Downloads, verifies and copies the new app next to `bundle`; returns the staged app.
    private nonisolated static func stage(_ update: AppUpdate, bundle: URL) async throws -> URL {
        let fm = FileManager.default
        let parent = bundle.deletingLastPathComponent()
        if let blocker = installBlocker(bundle: bundle, parentWritable: fm.isWritableFile(atPath: parent.path)) {
            throw SetupError(message: blocker)
        }
        let work = fm.temporaryDirectory.appendingPathComponent("herdr-update-\(UUID().uuidString)")
        try fm.createDirectory(at: work, withIntermediateDirectories: true)
        defer { try? fm.removeItem(at: work) }

        let (downloaded, response) = try await URLSession.shared.download(from: update.dmgURL)
        let dmg = work.appendingPathComponent(update.dmgName)
        try fm.moveItem(at: downloaded, to: dmg)
        guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
            throw SetupError(message: "アップデートをダウンロードできませんでした。")
        }
        var expected = update.sha256
        if expected == nil, let url = update.sha256URL {
            let (data, _) = try await URLSession.shared.data(from: url)
            expected = parseSha256File(String(decoding: data, as: UTF8.self), fileName: update.dmgName)
        }
        guard let expected else { throw SetupError(message: "\(update.dmgName)のチェックサムがリリースにありません。") }
        let actual = try SHA256.hash(data: Data(contentsOf: dmg, options: .mappedIfSafe)).map { String(format: "%02x", $0) }.joined()
        guard actual == expected else { throw SetupError(message: "ダウンロードした更新がチェックサムと一致しません。") }

        let mount = work.appendingPathComponent("mount")
        try fm.createDirectory(at: mount, withIntermediateDirectories: false)
        try run("/usr/bin/hdiutil", "attach", "-nobrowse", "-readonly", "-noautoopen", "-mountpoint", mount.path, dmg.path)
        defer {
            if (try? run("/usr/bin/hdiutil", "detach", mount.path)) == nil {
                _ = try? run("/usr/bin/hdiutil", "detach", "-force", mount.path)
            }
        }
        let app = mount.appendingPathComponent(appName)
        guard let info = NSDictionary(contentsOf: app.appendingPathComponent("Contents/Info.plist")) else {
            throw SetupError(message: "更新DMGに\(appName)がありません。")
        }
        guard info["CFBundleIdentifier"] as? String == bundleID else { throw SetupError(message: "更新がHerdr Companion Gatewayではありません。") }
        guard info["CFBundleShortVersionString"] as? String == update.version else {
            throw SetupError(message: "更新DMGにバージョン\(update.version)が含まれていません。")
        }
        try run("/usr/bin/codesign", "--verify", "--deep", "--strict", app.path)
        // Once the installed app carries a Developer ID, only the same team may replace it.
        if let team = teamIdentifier(bundle), teamIdentifier(app) != team {
            throw SetupError(message: "更新が別の開発者によって署名されています。")
        }
        let stage = parent.appendingPathComponent(".\(bundle.lastPathComponent).update")
        try? fm.removeItem(at: stage)
        try fm.createDirectory(at: stage, withIntermediateDirectories: false)
        let staged = stage.appendingPathComponent(bundle.lastPathComponent)
        try run("/usr/bin/ditto", app.path, staged.path)
        return staged
    }

    /// The signing team, or nil for an ad-hoc or unsigned app.
    private nonisolated static func teamIdentifier(_ app: URL) -> String? {
        guard let output = try? run("/usr/bin/codesign", "-dv", app.path) else { return nil }
        return output.split(whereSeparator: \.isNewline)
            .first { $0.hasPrefix("TeamIdentifier=") }
            .map { String($0.dropFirst("TeamIdentifier=".count)) }
            .flatMap { $0.isEmpty || $0 == "not set" ? nil : $0 }
    }

    /// Runs a system tool and returns its combined output, failing on a non-zero exit.
    @discardableResult
    private nonisolated static func run(_ executable: String, _ args: String...) throws -> String {
        let process = Process(); let pipe = Pipe()
        process.executableURL = URL(fileURLWithPath: executable); process.arguments = args
        process.standardOutput = pipe; process.standardError = pipe
        try process.run()
        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()
        let text = String(decoding: data, as: UTF8.self)
        guard process.terminationStatus == 0 else {
            throw SetupError(message: "\(URL(fileURLWithPath: executable).lastPathComponent)が失敗しました: \(text.prefix(300))")
        }
        return text
    }
}
