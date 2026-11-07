import XCTest
@testable import HerdrMenu

final class DeepSeekHarnessTests: XCTestCase {
    func test_OpenCodeGoの一覧からツールを使えるチャットモデルだけを名前順に取り出す() throws {
        let json = """
        {"object":"list","data":[
          {"id":"kimi-k3","name":"Kimi K3","type":"chat","capabilities":{"tools":{"supported":true}}},
          {"id":"deepseek-v4.1-flash","name":"DeepSeek V4.1 Flash","type":"chat","capabilities":{"tools":{"supported":true},"reasoning":{"supported":true}}},
          {"id":"no-tools","name":"No Tools","type":"chat","capabilities":{"tools":{"supported":false}}},
          {"id":"embed-1","name":"Embed","type":"embedding","capabilities":{"tools":{"supported":true}}},
          {"id":"bare","type":"chat"}
        ]}
        """
        let models = try decodeGoModels(Data(json.utf8))
        XCTAssertEqual(models.map(\.id), ["deepseek-v4.1-flash", "kimi-k3"])
        XCTAssertEqual(models.first?.name, "DeepSeek V4.1 Flash")
    }

    func test_選んだGoモデルを追加し外したものだけ消して他のルートは残す() {
        let existing = [
            DSHModelRoute(id: "openai-api", name: "OpenAI API", provider: "openai", model: "gpt-5.4"),
            DSHModelRoute(id: "my-kimi", name: "自分で付けた名前", provider: "opencode-go", model: "kimi-k3"),
            DSHModelRoute(id: "go-glm-5.3", name: "GLM 5.3 (Go)", provider: "opencode-go", model: "glm-5.3"),
        ]
        let catalog = [GoModel(id: "deepseek-v4.1-flash", name: "DeepSeek V4.1 Flash"), GoModel(id: "glm-5.3", name: "GLM 5.3"), GoModel(id: "kimi-k3", name: "Kimi K3")]
        let merged = mergeGoModels(existing: existing, selected: ["kimi-k3", "deepseek-v4.1-flash"], catalog: catalog)
        XCTAssertEqual(merged, [
            existing[0],
            existing[1],
            DSHModelRoute(id: "go-deepseek-v4.1-flash", name: "DeepSeek V4.1 Flash (Go)", provider: "opencode-go", model: "deepseek-v4.1-flash"),
        ])
    }

    func test_一覧を取得できなくても設定済みのGoモデルは保たれる() {
        let existing = [DSHModelRoute(id: "go-kimi-k3", name: "Kimi K3 (Go)", provider: "opencode-go", model: "kimi-k3")]
        XCTAssertEqual(mergeGoModels(existing: existing, selected: ["kimi-k3"], catalog: []), existing)
    }

    func test_既定モデルは残っていれば保ち消えたら最初のモデルか組み込みの既定に戻す() {
        let go = [DSHModelRoute(id: "go-kimi-k3", provider: "opencode-go", model: "kimi-k3")]
        XCTAssertEqual(defaultModel("deepseek-v4-pro", among: go), "deepseek-v4-pro")
        XCTAssertEqual(defaultModel("go-kimi-k3", among: go), "go-kimi-k3")
        XCTAssertEqual(defaultModel("go-removed", among: go), "go-kimi-k3")
        XCTAssertEqual(defaultModel("go-removed", among: []), "")
    }

    func test_Gatewayの状態JSONを読む() throws {
        let json = """
        {"binary":"/n/bin/dsh","configured":false,"version":"0.2.2-rc.1","keys":["opencode-go"],
         "models":[{"id":"go-kimi-k3","name":"Kimi K3 (Go)","provider":"opencode-go","model":"kimi-k3"}],"defaultModel":"go-kimi-k3"}
        """
        let status = try JSONDecoder().decode(DSHStatus.self, from: Data(json.utf8))
        XCTAssertEqual(status.binary, "/n/bin/dsh")
        XCTAssertEqual(status.keys, ["opencode-go"])
        XCTAssertEqual(status.models.first?.model, "kimi-k3")
        XCTAssertNil(status.error)
    }
}
