import AppKit
import SwiftUI
import CoreImage.CIFilterBuiltins
import ServiceManagement
import UniformTypeIdentifiers

@main
@MainActor
struct HerdrMenuApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var delegate
    @StateObject private var model: GatewayModel
    init() {
        // Start the owner at login even if the menu popover has never been opened.
        let owner = GatewayModel()
        _model = StateObject(wrappedValue: owner)
    }
    var body: some Scene {
        MenuBarExtra("Herdr Mobile", systemImage: "terminal") {
            Panel(model: model)
        }.menuBarExtraStyle(.window)
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        NotificationCenter.default.post(name: .init("HerdrMenuQuit"), object: nil)
        return .terminateNow
    }
}

struct Invitation: Decodable { let code: String; let expiresAt: Date }
struct LocalConfig: Decodable {
    let authToken: String
    let firebaseAndroid: AndroidFirebase?
    let jevApiKey: String?
}
struct AndroidFirebase: Decodable { let projectId: String }

struct SetupError: LocalizedError {
    let message: String
    var errorDescription: String? { message }
}

@MainActor final class GatewayModel: ObservableObject {
    @Published var address = UserDefaults.standard.string(forKey: "address") ?? ""
    @Published var port = UserDefaults.standard.string(forKey: "port") ?? "8766"
    @Published var status = "停止中"
    @Published var running = false
    @Published var busy = false
    @Published var error: String?
    @Published var firebaseProject: String?
    @Published var jevKey = ""
    @Published var jevEnabled = false
    @Published var qr: NSImage?
    @Published var pairingLink: String?
    @Published var expires: Date?
    @Published var serviceAccount: URL?
    @Published var androidConfig: URL?
    @Published var loginEnabled = SMAppService.mainApp.status == .enabled
    @Published var diagnostics = "Herdr・Tailscaleは別途インストールが必要です。"
    private var child: Process?
    private var timer: Timer?
    private var quitObserver: NSObjectProtocol?
    private let fm = FileManager.default
    private var home: URL { fm.homeDirectoryForCurrentUser }
    var configURL: URL { home.appendingPathComponent(".config/herdr-mobile/desktop/config.json") }
    var stateURL: URL { home.appendingPathComponent(".local/state/herdr-mobile/desktop") }
    var baseURL: URL? {
        guard validAddress(address), let n = Int(port), (1024...65535).contains(n) else { return nil }
        return URL(string: "http://\(address):\(n)")
    }
    private var executable: URL? { Bundle.main.url(forResource: "herdr-mobile-gateway", withExtension: nil) }
    private var environment: [String: String] {
        var env = ProcessInfo.processInfo.environment
        for key in Array(env.keys) where key.hasPrefix("HERDR_") || key.hasPrefix("CLAUDE_CODE_") || key == "CLAUDECODE" || key == "TYPESAFE_API_KEY" || key == "GOOGLE_APPLICATION_CREDENTIALS" {
            env.removeValue(forKey: key)
        }
        env["PATH"] = "\(home.path)/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
        env["HERDR_MOBILE_CONFIG"] = configURL.path
        env["HERDR_MOBILE_STATE_DIR"] = stateURL.path
        return env
    }
    init() {
        quitObserver = NotificationCenter.default.addObserver(forName: .init("HerdrMenuQuit"), object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.child?.terminate() }
        }
        timer = Timer.scheduledTimer(withTimeInterval: 2, repeats: true) { [weak self] _ in
            Task { @MainActor in
                guard let self else { return }
                if let expires = self.expires, expires <= Date() { self.clearQR() }
            }
        }
        Task {
            await detectAddress()
            loadConfig()
            if UserDefaults.standard.bool(forKey: "autoStart") { perform { await self.start() } }
        }
    }
    func validAddress(_ input: String) -> Bool {
        let octets = input.split(separator: ".").compactMap { Int($0) }
        return octets.count == 4 && octets[0] == 100 && (64...127).contains(octets[1]) && octets.allSatisfy { (0...255).contains($0) } && input == octets.map(String.init).joined(separator: ".")
    }
    func perform(_ operation: @escaping () async throws -> Void) {
        guard !busy else { return }
        busy = true; error = nil
        Task {
            defer { busy = false }
            do { try await operation() } catch { self.error = error.localizedDescription }
        }
    }
    func loadConfig() {
        if let data = try? Data(contentsOf: configURL), let config = try? JSONDecoder().decode(LocalConfig.self, from: data) {
            firebaseProject = config.firebaseAndroid?.projectId
            jevEnabled = !(config.jevApiKey ?? "").isEmpty
        }
    }
    func command(_ args: [String], input: String? = nil) async throws -> String {
        guard let executable else { throw SetupError(message: "同梱Gatewayが見つかりません。DMG版を使用してください。") }
        return try await Self.run(executable, args, environment, input: input)
    }
    nonisolated static func run(_ executable: URL, _ args: [String], _ env: [String: String], input: String? = nil) async throws -> String {
        try await Task.detached {
            let process = Process(); let pipe = Pipe()
            process.executableURL = executable; process.arguments = args; process.environment = env
            process.standardOutput = pipe; process.standardError = pipe
            let stdin = Pipe()
            process.standardInput = stdin
            try process.run()
            if let input { stdin.fileHandleForWriting.write(Data(input.utf8)) }
            try stdin.fileHandleForWriting.close()
            // Bound external tool execution without blocking the UI.
            let timeout = DispatchWorkItem { if process.isRunning { process.terminate() } }
            DispatchQueue.global().asyncAfter(deadline: .now() + 20, execute: timeout)
            let data = pipe.fileHandleForReading.readDataToEndOfFile()
            process.waitUntilExit(); timeout.cancel()
            let text = String(data: data, encoding: .utf8) ?? ""
            guard process.terminationStatus == 0 else { throw SetupError(message: text.isEmpty ? "コマンドを実行できませんでした。" : text) }
            return text.trimmingCharacters(in: .whitespacesAndNewlines)
        }.value
    }
    func detectAddress() async {
        let candidates = ["/Applications/Tailscale.app/Contents/MacOS/Tailscale", "/opt/homebrew/bin/tailscale", "/usr/local/bin/tailscale"]
        for path in candidates where fm.isExecutableFile(atPath: path) {
            if let value = try? await Self.run(URL(fileURLWithPath: path), ["ip", "-4"], environment), validAddress(value) {
                if address.isEmpty { address = value }
                diagnostics = "Tailscale: \(value)\nHerdrの起動とClaude/Codex integrationの設定を確認してください。"
                return
            }
        }
        diagnostics = "TailscaleのIPv4を入力してください。MacとAndroidを同じtailnetに接続します。"
    }
    func start() async {
        guard child == nil else { return }
        guard let baseURL, let executable else { error = "Tailscale IPv4とポートを確認してください。"; return }
        do {
            _ = try await command(["token"])
            try fm.createDirectory(at: stateURL, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
            let log = stateURL.appendingPathComponent("desktop.log")
            if !fm.fileExists(atPath: log.path) { fm.createFile(atPath: log.path, contents: nil, attributes: [.posixPermissions: 0o600]) }
            let handle = try FileHandle(forWritingTo: log); try handle.truncate(atOffset: 0)
            let process = Process()
            process.executableURL = executable
            process.arguments = ["serve", "--listen", "\(address):\(port)", "--parent-pid", String(ProcessInfo.processInfo.processIdentifier)]
            // Runtime JSON logs already rotate in gateway.log; only startup errors go here.
            process.environment = environment; process.standardOutput = FileHandle.nullDevice; process.standardError = handle
            process.terminationHandler = { [weak self] proc in
                try? handle.close()
                Task { @MainActor in
                    guard let self, self.child === proc else { return }
                    self.child = nil; self.running = false; self.status = "停止中"; self.clearQR()
                    if proc.terminationStatus != 0 { self.error = "Gatewayを起動できませんでした。ポートの重複やログを確認してください。" }
                }
            }
            try process.run(); child = process; status = "起動中…"
            UserDefaults.standard.set(address, forKey: "address"); UserDefaults.standard.set(port, forKey: "port")
            for _ in 0..<25 {
                try await Task.sleep(nanoseconds: 200_000_000)
                guard child === process, process.isRunning else { return }
                // Authenticated endpoint prevents mistaking a different server for our child.
                if (try? await request("/v1/pairing", method: "DELETE", base: baseURL)) != nil {
                    running = true; status = "接続待機中"; UserDefaults.standard.set(true, forKey: "autoStart"); return
                }
            }
            await stop(); error = "Gatewayの起動確認がタイムアウトしました。ログを確認してください。"
        } catch { self.error = error.localizedDescription }
    }
    func stop() async {
        UserDefaults.standard.set(false, forKey: "autoStart")
        clearQR()
        guard let process = child else { return }
        process.terminate()
        for _ in 0..<35 {
            if !process.isRunning { break }
            try? await Task.sleep(nanoseconds: 200_000_000)
        }
        if process.isRunning { error = "Gatewayの終了を待っています。再起動は終了後に行ってください。"; return }
        if child === process { child = nil }
        running = false; status = "停止中"
    }
    func request(_ path: String, method: String, base: URL? = nil) async throws -> Data {
        guard let url = (base ?? baseURL)?.appendingPathComponent(String(path.dropFirst())) else { throw SetupError(message: "接続先が未設定です。") }
        let config = try JSONDecoder().decode(LocalConfig.self, from: Data(contentsOf: configURL))
        var request = URLRequest(url: url); request.httpMethod = method; request.timeoutInterval = 3
        request.setValue("Bearer \(config.authToken)", forHTTPHeaderField: "Authorization")
        let (data, response) = try await URLSession.shared.data(for: request)
        guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else { throw SetupError(message: "Gatewayに接続できません。起動状態を確認してください。") }
        return data
    }
    func createQR() async throws {
        guard running, let baseURL else { throw SetupError(message: "Gatewayを起動してください。") }
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .custom { decoder in
            let text = try decoder.singleValueContainer().decode(String.self)
            let formatter = ISO8601DateFormatter()
            formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            if let date = formatter.date(from: text) { return date }
            formatter.formatOptions = [.withInternetDateTime]
            guard let date = formatter.date(from: text) else { throw SetupError(message: "QRの有効期限を読み取れませんでした。") }
            return date
        }
        let invitation = try decoder.decode(Invitation.self, from: await request("/v1/pairing", method: "POST"))
        var url = URLComponents(); url.scheme = "herdr-mobile"; url.host = "pair"
        url.queryItems = [URLQueryItem(name: "url", value: baseURL.absoluteString)]
        url.fragment = invitation.code
        guard let link = url.string else { return }
        let filter = CIFilter.qrCodeGenerator(); filter.message = Data(link.utf8); filter.correctionLevel = "M"
        guard let output = filter.outputImage?.transformed(by: CGAffineTransform(scaleX: 6, y: 6)), let cg = CIContext().createCGImage(output, from: output.extent) else { throw SetupError(message: "QRコードを生成できませんでした。") }
        qr = NSImage(cgImage: cg, size: NSSize(width: output.extent.width, height: output.extent.height))
        pairingLink = link; expires = invitation.expiresAt
    }
    func clearQR() { qr = nil; pairingLink = nil; expires = nil }
    func cancelQR() async throws { _ = try await request("/v1/pairing", method: "DELETE"); clearQR() }
    func selectJSON(service: Bool) {
        let panel = NSOpenPanel(); panel.allowedContentTypes = [.json]; panel.allowsMultipleSelection = false
        panel.message = service ? "Firebaseのサービスアカウント秘密鍵JSONを選択" : "同じプロジェクトのgoogle-services.jsonを選択"
        NSApp.activate(ignoringOtherApps: true)
        if panel.runModal() == .OK { if service { serviceAccount = panel.url } else { androidConfig = panel.url } }
    }
    func importFirebase() async throws {
        guard let serviceAccount, let androidConfig else { return }
        let wasRunning = running
        await stop()
        guard child == nil else { throw SetupError(message: "Gatewayの停止後にもう一度取り込んでください。") }
        do {
            _ = try await command(["import-firebase", "--service-account", serviceAccount.path, "--android-config", androidConfig.path])
            self.serviceAccount = nil; self.androidConfig = nil; loadConfig()
        } catch {
            if wasRunning { await start() }
            throw error
        }
        if wasRunning { await start() }
    }
    func setLogin(_ enabled: Bool) {
        do {
            if enabled { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }
            loginEnabled = SMAppService.mainApp.status == .enabled
            if enabled && !loginEnabled { error = "システム設定 → 一般 → ログイン項目でHerdr Mobileを許可してください。" }
        } catch { self.error = error.localizedDescription }
    }
    func saveJev(remove: Bool = false) async throws {
        guard remove || !jevKey.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
        guard jevKey.utf8.count <= 4096 else { throw SetupError(message: "APIキーが長すぎます。") }
        let wasRunning = running
        await stop()
        guard child == nil else { throw SetupError(message: "Gatewayの終了を待ってください。") }
        do {
            _ = try await command(["set-jev-key"], input: remove ? "" : jevKey)
            jevKey = ""; loadConfig()
        } catch {
            if wasRunning { await start() }
            throw error
        }
        if wasRunning { await start() }
    }
}

struct Panel: View {
    @ObservedObject var model: GatewayModel
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                HStack {
                    Image(systemName: "terminal.fill").font(.title)
                    VStack(alignment: .leading) {
                        Text("Herdr Mobile").font(.headline)
                        Label(model.status, systemImage: model.running ? "circle.fill" : "circle")
                            .foregroundStyle(model.running ? .green : .secondary).font(.caption)
                    }
                    Spacer()
                    Button("終了") { NSApp.terminate(nil) }
                }
                GroupBox("Macへの接続") {
                    VStack(alignment: .leading, spacing: 8) {
                        TextField("Tailscale IPv4（100.x.x.x）", text: $model.address).disabled(model.running || model.busy)
                        TextField("ポート", text: $model.port).disabled(model.running || model.busy)
                        Text(model.diagnostics).font(.caption).foregroundStyle(.secondary)
                        HStack {
                            Button(model.running ? "停止" : "Gatewayを起動") {
                                model.perform { if model.running { await model.stop() } else { await model.start() } }
                            }.buttonStyle(.borderedProminent)
                            Button("IPを検出") { model.perform { await model.detectAddress() } }.disabled(model.running)
                            Button("ログ") { NSWorkspace.shared.open(model.stateURL) }
                        }
                    }.padding(6)
                }
                GroupBox("通知 · Firebaseを持ち込む") {
                    VStack(alignment: .leading, spacing: 8) {
                        Text(model.firebaseProject.map { "設定済み: \($0)" } ?? "未設定でも閲覧・操作は利用できます。")
                        Text("同じFirebaseプロジェクトの2ファイルを選びます。秘密鍵はこのMacだけに保存します。Androidのパッケージ名: com.tohutohu.herdrmobile")
                            .font(.caption).foregroundStyle(.secondary).textSelection(.enabled)
                        Button(model.serviceAccount?.lastPathComponent ?? "① サービスアカウントJSONを選択") { model.selectJSON(service: true) }
                        Button(model.androidConfig?.lastPathComponent ?? "② google-services.jsonを選択") { model.selectJSON(service: false) }
                        Button("取り込む") { model.perform { try await model.importFirebase() } }
                            .disabled(model.serviceAccount == nil || model.androidConfig == nil)
                        Text("取り込み後にAndroidをペアリングしてください。").font(.caption)
                    }.padding(6)
                }
                GroupBox("Androidとペアリング") {
                    VStack(alignment: .leading, spacing: 8) {
                        Text("Androidの設定 →「Scan Mac QR」で読み取ります。両方のTailscaleをオンにしてください。")
                            .font(.caption).foregroundStyle(.secondary)
                        Button("ペアリングQRを表示") { model.perform { try await model.createQR() } }.disabled(!model.running)
                        if let qr = model.qr, let expires = model.expires {
                            HStack { Spacer(); Image(nsImage: qr).interpolation(.none).resizable().scaledToFit().frame(width: 230, height: 230).padding(24).background(.white); Spacer() }
                            Text("有効期限 \(expires.formatted(date: .omitted, time: .shortened)) · 1回限り").font(.caption)
                            Text("読み取った端末から、このMacのセッションを閲覧・操作できます。").font(.caption)
                            Button("QRを無効化") { model.perform { try await model.cancelQR() } }
                        }
                    }.padding(6)
                }
                GroupBox("フォルダ確認 · Jev（任意）") {
                    VStack(alignment: .leading, spacing: 8) {
                        Text( model.jevEnabled ? "設定済み" : "未設定 · 通常の操作には不要です") .font(.caption)
                        SecureField("TypeSafe APIキー", text: $model.jevKey)
                        Text("有効にすると指示、フォルダ情報、READMEなどと最近の会話の抜粋をTypeSafeへ送ります。キーはMacだけに保存します。")
                            .font(.caption).foregroundStyle(.secondary)
                        HStack {
                            Button("保存して有効化") { model.perform { try await model.saveJev() } }.disabled(model.jevKey.isEmpty)
                            if model.jevEnabled { Button("キーを削除") { model.perform { try await model.saveJev(remove: true) } } }
                        }
                    }.padding(6)
                }
                Toggle("ログイン時に起動", isOn: Binding(get: { model.loginEnabled }, set: { model.setLogin($0) }))
                if model.busy { ProgressView().controlSize(.small) }
                if let error = model.error { Text(error).font(.caption).foregroundStyle(.red).textSelection(.enabled) }
            }.padding(18).disabled(model.busy)
        }.frame(width: 430, height: 700)
    }
}
