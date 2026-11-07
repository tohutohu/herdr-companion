import AppKit
import SwiftUI

/// One entry of the Gateway's dshModels: an app-visible model id and the DSH route behind it.
struct DSHModelRoute: Codable, Hashable {
    var id: String
    var name: String?
    var provider: String
    var model: String
    var efforts: [String]?
}

/// `herdr-mobile-gateway dsh status`; never contains a key.
struct DSHStatus: Decodable {
    var binary: String?
    var configured = false
    var version: String?
    var error: String?
    var keys: [String] = []
    var models: [DSHModelRoute] = []
    var defaultModel = "deepseek-flash"
}

enum DSHService: String, CaseIterable, Identifiable {
    case openCodeGo = "opencode-go"
    case deepSeek = "deepseek-official"
    var id: String { rawValue }
    var label: String {
        switch self {
        case .openCodeGo: "OpenCode Go"
        case .deepSeek: "DeepSeek API（公式）"
        }
    }
}

/// Models the Gateway always offers through DeepSeek's own API.
let builtInDSHModels = ["deepseek-flash", "deepseek-v4-pro"]
let openCodeGoModelsURL = URL(string: "https://opencode.ai/inference/go/v1/models")!

/// A model from OpenCode Go's public list.
struct GoModel: Identifiable, Hashable {
    let id: String
    let name: String
}

private struct GoModelList: Decodable {
    struct Entry: Decodable {
        struct Capabilities: Decodable {
            struct Tools: Decodable { let supported: Bool? }
            let tools: Tools?
        }
        let id: String
        let name: String?
        let type: String?
        let capabilities: Capabilities?
    }
    let data: [Entry]
}

/// Chat models that can call tools, by name. An agent cannot work without tools.
func decodeGoModels(_ data: Data) throws -> [GoModel] {
    try JSONDecoder().decode(GoModelList.self, from: data).data
        .filter { ($0.type ?? "chat") == "chat" && $0.capabilities?.tools?.supported == true }
        .map { GoModel(id: $0.id, name: $0.name ?? $0.id) }
        .sorted { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
}

/// dshModels after choosing OpenCode Go models. Other routes and the existing
/// entries for chosen models are kept unchanged; new ones get a "go-" id.
func mergeGoModels(existing: [DSHModelRoute], selected: Set<String>, catalog: [GoModel]) -> [DSHModelRoute] {
    let go = DSHService.openCodeGo.rawValue
    var out = existing.filter { $0.provider != go || selected.contains($0.model) }
    let present = Set(out.filter { $0.provider == go }.map(\.model))
    let names = Dictionary(catalog.map { ($0.id, $0.name) }, uniquingKeysWith: { a, _ in a })
    let order = catalog.map(\.id) + selected.subtracting(catalog.map(\.id)).sorted()
    for model in order where selected.contains(model) && !present.contains(model) {
        out.append(DSHModelRoute(id: "go-\(model)", name: "\(names[model] ?? model) (Go)", provider: go, model: model))
    }
    return out
}

/// Keeps the default model if it is still offered, otherwise the first chosen
/// model; "" is the Gateway's built-in default.
func defaultModel(_ current: String, among models: [DSHModelRoute]) -> String {
    if builtInDSHModels.contains(current) || models.contains(where: { $0.id == current }) { return current }
    return models.first?.id ?? ""
}

extension GatewayModel {
    var goModelsConfigured: Set<String> {
        Set(dsh.models.filter { $0.provider == DSHService.openCodeGo.rawValue }.map(\.model))
    }
    var dshPreviewModels: [DSHModelRoute] { mergeGoModels(existing: dsh.models, selected: dshSelected, catalog: goModels) }
    var dshChanged: Bool {
        !dshKey.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || dshSelected != goModelsConfigured || dshDefault != dsh.defaultModel
    }

    func refreshDSH() async {
        do {
            dsh = try JSONDecoder().decode(DSHStatus.self, from: Data(try await command(["dsh", "status"]).utf8))
            dshSelected = goModelsConfigured
            dshDefault = dsh.defaultModel
            dshLoaded = true
            preselectDeepSeek()
        } catch {
            dsh.error = error.localizedDescription
        }
    }

    func loadGoModels() async {
        guard goModels.isEmpty, !goModelsLoading else { return }
        goModelsLoading = true
        defer { goModelsLoading = false }
        do {
            var request = URLRequest(url: openCodeGoModelsURL)
            request.timeoutInterval = 15
            let (data, response) = try await URLSession.shared.data(for: request)
            guard (response as? HTTPURLResponse)?.statusCode == 200 else { throw SetupError(message: "") }
            goModels = try decodeGoModels(data)
            goModelsError = nil
            preselectDeepSeek()
        } catch {
            goModelsError = "OpenCode Goのモデル一覧を取得できませんでした。ネットワークを確認して再試行してください。"
        }
    }

    /// The first setup offers Go's DeepSeek models, which are why it is used.
    private func preselectDeepSeek() {
        guard dshLoaded, goModelsConfigured.isEmpty, dshSelected.isEmpty, !dsh.keys.contains(DSHService.openCodeGo.rawValue) else { return }
        dshSelected = Set(goModels.map(\.id).filter { $0.hasPrefix("deepseek-") })
        if let first = dshPreviewModels.first(where: { $0.provider == DSHService.openCodeGo.rawValue }) { dshDefault = first.id }
    }

    /// Saves the key, chosen models and default together, restarting a running Gateway once.
    func saveDSH() async throws {
        var change: [String: Any] = [:]
        let key = dshKey.trimmingCharacters(in: .whitespacesAndNewlines)
        guard key.utf8.count <= 4096 else { throw SetupError(message: "APIキーが長すぎます。") }
        if !key.isEmpty { change["keys"] = [dshService.rawValue: key] }
        let go = DSHService.openCodeGo
        if !dshSelected.isEmpty, !dsh.keys.contains(go.rawValue), !(dshService == go && !key.isEmpty) {
            throw SetupError(message: "OpenCode GoのAPIキーを入力してから保存してください。")
        }
        let models = dshPreviewModels
        if dshSelected != goModelsConfigured {
            change["models"] = try JSONSerialization.jsonObject(with: JSONEncoder().encode(models))
        }
        let chosen = defaultModel(dshDefault, among: models)
        if chosen != dsh.defaultModel { change["defaultModel"] = chosen }
        try await configureDSH(change)
        dshKey = ""
    }

    func removeDSHKey() async throws { try await configureDSH(["keys": [dshService.rawValue: ""]]) }

    func chooseDSHBinary() async throws {
        let panel = NSOpenPanel()
        panel.canChooseFiles = true; panel.canChooseDirectories = false; panel.allowsMultipleSelection = false
        // The npm command is a symlink next to Node; resolving it would lose that Node.
        panel.resolvesAliases = false; panel.showsHiddenFiles = true
        panel.message = "dsh の実行ファイルを選択（例: ~/.local/share/mise/installs/node/<version>/bin/dsh）"
        let mise = home.appendingPathComponent(".local/share/mise/installs/node")
        if FileManager.default.fileExists(atPath: mise.path) { panel.directoryURL = mise }
        NSApp.activate(ignoringOtherApps: true)
        guard panel.runModal() == .OK, let url = panel.url else { return }
        try await configureDSH(["binary": url.path])
    }

    func resetDSHBinary() async throws { try await configureDSH(["binary": ""]) }

    /// The Gateway reads these settings at start, so a running one is restarted.
    private func configureDSH(_ change: [String: Any]) async throws {
        guard !change.isEmpty else { return }
        let input = String(decoding: try JSONSerialization.data(withJSONObject: change), as: UTF8.self)
        let wasRunning = running
        await stop()
        guard !running else { throw SetupError(message: "Gatewayの終了を待ってください。") }
        do {
            _ = try await command(["dsh", "configure"], input: input)
        } catch {
            if wasRunning { await start() }
            throw error
        }
        await refreshDSH()
        if wasRunning { await start() }
    }
}

struct DSHBox: View {
    @ObservedObject var model: GatewayModel
    private var keySet: Bool { model.dsh.keys.contains(model.dshService.rawValue) }
    var body: some View {
        GroupBox("DeepSeek Harness（任意）") {
            VStack(alignment: .leading, spacing: 8) {
                binary
                Picker("サービス", selection: $model.dshService) {
                    ForEach(DSHService.allCases) { Text($0.label).tag($0) }
                }
                Text(keySet ? "APIキー: 設定済み" : "APIキー: 未設定").font(.caption)
                SecureField(keySet ? "新しいAPIキーで置き換える" : "APIキー", text: $model.dshKey)
                if model.dshService == .openCodeGo { goModels } else {
                    Text("deepseek-flash と deepseek-v4-pro をDeepSeekのAPIで使います（従量課金）。")
                        .font(.caption).foregroundStyle(.secondary)
                }
                Picker("新しいセッションの既定", selection: $model.dshDefault) {
                    ForEach(builtInDSHModels, id: \.self) { Text($0).tag($0) }
                    ForEach(model.dshPreviewModels, id: \.id) { Text($0.name ?? $0.id).tag($0.id) }
                    if !model.dshDefault.isEmpty, !builtInDSHModels.contains(model.dshDefault), !model.dshPreviewModels.contains(where: { $0.id == model.dshDefault }) {
                        Text(model.dshDefault).tag(model.dshDefault)
                    }
                }
                HStack {
                    Button(model.running ? "保存してGatewayを再起動" : "保存") { model.perform { try await model.saveDSH() } }
                        .disabled(!model.dshChanged || model.dsh.binary == nil)
                    if keySet { Button("キーを削除") { model.perform { try await model.removeDSHKey() } } }
                }
                Text("キーはこのMacのGateway設定だけに保存し、DSHを起動するときに環境変数で渡します。再起動で実行中のセッションは止まり、次のメッセージで再開します。")
                    .font(.caption).foregroundStyle(.secondary)
            }.padding(6)
        }
        .task { if !model.dshLoaded { await model.refreshDSH() } }
    }

    @ViewBuilder private var binary: some View {
        if let path = model.dsh.binary, model.dsh.error == nil {
            Text("dsh \(model.dsh.version ?? "") · \(model.dsh.configured ? "指定" : "自動検出")").font(.caption)
            Text(path).font(.caption2).foregroundStyle(.secondary).textSelection(.enabled).lineLimit(2)
        } else if model.dshLoaded && model.dsh.binary == nil {
            Text("dshが見つかりません。インストールしてから「再検出」を押してください。").font(.caption).foregroundStyle(.red)
            Text("npm install --global @deepseek-ai/dsh@0.2.2-rc.1").font(.caption2).textSelection(.enabled)
        } else if let error = model.dsh.error {
            Text(error).font(.caption).foregroundStyle(.red).textSelection(.enabled)
        }
        HStack {
            Button("再検出") { model.perform { await model.refreshDSH() } }
            Button("選択…") { model.perform { try await model.chooseDSHBinary() } }
            if model.dsh.configured { Button("自動検出に戻す") { model.perform { try await model.resetDSHBinary() } } }
        }
    }

    private var goModels: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("アプリのモデル選択に出すOpenCode Goのモデル").font(.caption)
            if model.goModels.isEmpty {
                if let error = model.goModelsError {
                    Text(error).font(.caption).foregroundStyle(.red)
                    Button("再試行") { Task { await model.loadGoModels() } }
                } else {
                    ProgressView().controlSize(.small)
                }
            } else {
                ScrollView {
                    VStack(alignment: .leading, spacing: 2) {
                        ForEach(model.goModels) { m in
                            Toggle(isOn: Binding(
                                get: { model.dshSelected.contains(m.id) },
                                set: { if $0 { model.dshSelected.insert(m.id) } else { model.dshSelected.remove(m.id) } }
                            )) { Text(m.name).help(m.id) }
                        }
                    }.frame(maxWidth: .infinity, alignment: .leading)
                }.frame(height: 150)
            }
            Text("DeepSeekのモデルは、OpenCode ConsoleのSettings › Privacy › RegionsでGlobalを有効にすると使えます。料金はGoの契約に含まれます。")
                .font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
        }.task { await model.loadGoModels() }
    }
}
