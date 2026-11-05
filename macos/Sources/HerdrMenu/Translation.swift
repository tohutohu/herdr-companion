import AppKit
import SwiftUI

struct TranslationStatus: Decodable {
    var model = "0.8b"
    var input = false
    var output = false
    var autoStart = false
    var supported = false
    var state = "stopped"
    var detail = "Gatewayを起動してください"
    var error: String?
}

extension GatewayModel {
    func refreshTranslation() async {
        guard running, !translationPolling else { return }
        translationPolling = true
        defer { translationPolling = false }
        do {
            let next = try JSONDecoder().decode(TranslationStatus.self, from: await request("/v1/translation", method: "GET"))
            if !translationLoaded || translationChoice == translation.model {
                translationChoice = next.model
                translationLoaded = true
            }
            translation = next
        } catch {
            translation.detail = "翻訳の状態を取得できませんでした"
        }
    }

    func translationAction(_ action: String, input: Bool? = nil, output: Bool? = nil) async throws {
        var body: [String: Any] = ["action": action]
        if action == "start" { body["model"] = translationChoice }
        if action == "configure" {
            body["input"] = input ?? translation.input
            body["output"] = output ?? translation.output
        }
        let data = try await request("/v1/translation", method: "POST", body: JSONSerialization.data(withJSONObject: body))
        translation = try JSONDecoder().decode(TranslationStatus.self, from: data)
    }
}

struct TranslationBox: View {
    @ObservedObject var model: GatewayModel
    private var active: Bool { model.translation.state == "ready" || model.translation.state == "starting" }
    var body: some View {
        GroupBox("ローカル翻訳 · PLaMo 3 Translate") {
            VStack(alignment: .leading, spacing: 8) {
                Text(model.translation.detail).font(.caption)
                Picker("モデル（MLX 4bit）", selection: $model.translationChoice) {
                    Text("0.8B · 約1 GB").tag("0.8b")
                    Text("2B · 約2.4 GB").tag("2b")
                    Text("8B · 約6.1 GB").tag("8b")
                }
                .disabled(!model.running || model.busy || model.translation.state == "starting")
                HStack {
                    Button(active && model.translation.model == model.translationChoice ? "翻訳を停止" : "ダウンロードして起動") {
                        model.perform {
                            try await model.translationAction(active && model.translation.model == model.translationChoice ? "stop" : "start")
                        }
                    }.disabled(!model.running || !model.translation.supported || model.busy)
                    if model.translation.state == "starting" { ProgressView().controlSize(.small) }
                    Button("ログ") { NSWorkspace.shared.open(model.stateURL.appendingPathComponent("translation")) }
                }
                Toggle("プロンプトを日本語→英語に翻訳して送信", isOn: Binding(
                    get: { model.translation.input },
                    set: { value in model.perform { try await model.translationAction("configure", input: value) } }
                )).disabled(!model.running || !model.translation.supported)
                Toggle("報告・承認ダイアログ・タイトルを日本語で表示", isOn: Binding(
                    get: { model.translation.output },
                    set: { value in model.perform { try await model.translationAction("configure", output: value) } }
                ))
                Text("セッションごとのTranslation mode指定が優先され、指定のないセッションではこの設定を使います。報告本文はターン完了後に翻訳します。コード・ツール結果・承認の操作値はそのまま保持し、承認ダイアログの表示文も原文に戻して確認できます。")
                    .font(.caption).foregroundStyle(.secondary)
                Text("保存済みの訳はモデル停止中も再利用します。新しい翻訳が必要な場合は、翻訳できるまで送信しません。自動起動が有効なら異常終了後も次の翻訳要求で再起動します。").font(.caption).foregroundStyle(.secondary)
                Text("Apple silicon Mac専用。初回はPython・MLX・PFN公式モデルをダウンロードします。導入後の翻訳はMac内で動き、次回のGateway起動時にも自動起動します。")
                    .font(.caption).foregroundStyle(.secondary)
                Link("モデル・PLaMo Community License", destination: URL(string: "https://huggingface.co/pfnet/plamo-3-translate-0.8b-MLX-4bit")!)
                    .font(.caption)
                if let error = model.translation.error { Text(error).font(.caption).foregroundStyle(.red).textSelection(.enabled) }
            }.padding(6)
            .disabled(model.busy)
        }
    }
}
