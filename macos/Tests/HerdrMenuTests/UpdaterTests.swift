import XCTest
@testable import HerdrMenu

final class UpdaterTests: XCTestCase {
    private let sha = "3f9dfbea20d3c5e12a41490c79f5e22e9babb101c661d64759b0097fb0103f43"

    private func release(tag: String = "v1.2.0", digest: String? = nil, checksumFile: Bool = true, prerelease: Bool = false) -> GitHubRelease {
        let version = tag.hasPrefix("v") ? String(tag.dropFirst()) : tag
        var assets = [
            GitHubAsset(name: "Herdr-Companion-\(version).dmg", downloadURL: "https://example.test/ui.dmg", digest: "sha256:\(String(repeating: "0", count: 64))"),
            GitHubAsset(name: "Herdr-Companion-Gateway-\(version).dmg", downloadURL: "https://example.test/gateway.dmg", digest: digest),
        ]
        if checksumFile {
            assets.append(GitHubAsset(name: "Herdr-Companion-Gateway-\(version).dmg.sha256", downloadURL: "https://example.test/gateway.dmg.sha256"))
        }
        return GitHubRelease(tagName: tag, htmlURL: "https://github.com/tohutohu/herdr-companion/releases/tag/\(tag)", prerelease: prerelease, assets: assets)
    }

    func test_バージョンは数値として比較し足りない桁は0とみなす() {
        XCTAssertEqual(compareVersions("1.10.0", "1.9.9"), 1)
        XCTAssertEqual(compareVersions("v1.2", "1.2.0"), 0)
        XCTAssertEqual(compareVersions("1.1.0", "2"), -1)
        XCTAssertNil(compareVersions("1.2.0-beta", "1.1.0"))
        XCTAssertNil(compareVersions("1.2.3.4", "1.1.0"))
    }

    func test_新しいリリースではGatewayのDMGとGitHubのダイジェストを選ぶ() throws {
        let update = try XCTUnwrap(selectUpdate(release(digest: "sha256:\(sha.uppercased())"), current: "1.1.0", dmgPrefix: "Herdr-Companion-Gateway"))
        XCTAssertEqual(update.version, "1.2.0")
        XCTAssertEqual(update.dmgName, "Herdr-Companion-Gateway-1.2.0.dmg")
        XCTAssertEqual(update.dmgURL.absoluteString, "https://example.test/gateway.dmg")
        XCTAssertEqual(update.sha256, sha)
    }

    func test_同じか古いリリースとプレリリースは更新として扱わない() {
        XCTAssertNil(selectUpdate(release(tag: "v1.1.0"), current: "1.1.0", dmgPrefix: "Herdr-Companion-Gateway"))
        XCTAssertNil(selectUpdate(release(tag: "v1.0.0"), current: "1.1.0", dmgPrefix: "Herdr-Companion-Gateway"))
        XCTAssertNil(selectUpdate(release(prerelease: true), current: "1.1.0", dmgPrefix: "Herdr-Companion-Gateway"))
    }

    func test_ダイジェストがなければチェックサムファイルで検証し無ければ更新しない() throws {
        let update = try XCTUnwrap(selectUpdate(release(), current: "1.1.0", dmgPrefix: "Herdr-Companion-Gateway"))
        XCTAssertNil(update.sha256)
        XCTAssertEqual(update.sha256URL?.absoluteString, "https://example.test/gateway.dmg.sha256")
        XCTAssertNil(selectUpdate(release(checksumFile: false), current: "1.1.0", dmgPrefix: "Herdr-Companion-Gateway"))
    }

    func test_チェックサムファイルは対象ファイルの行だけを読む() {
        let name = "Herdr-Companion-Gateway-1.2.0.dmg"
        let text = "\(String(repeating: "a", count: 64))  Herdr-Companion-1.2.0.dmg\n\(sha)  \(name)\n"
        XCTAssertEqual(parseSha256File(text, fileName: name), sha)
        XCTAssertEqual(parseSha256File("\(sha.uppercased()) *\(name)", fileName: name), sha)
        XCTAssertNil(parseSha256File("\(sha)  other.dmg", fileName: name))
        XCTAssertNil(parseSha256File("abc  \(name)", fileName: name))
    }

    func test_DMGや隔離された場所から起動したアプリは置き換えない() {
        XCTAssertNotNil(installBlocker(bundle: URL(fileURLWithPath: "/Volumes/Herdr/Herdr Companion Gateway.app"), parentWritable: false))
        XCTAssertNotNil(installBlocker(bundle: URL(fileURLWithPath: "/private/var/folders/x/AppTranslocation/A/d/Herdr Companion Gateway.app"), parentWritable: true))
        XCTAssertNotNil(installBlocker(bundle: URL(fileURLWithPath: "/Applications/Herdr Companion Gateway.app"), parentWritable: false))
        XCTAssertNil(installBlocker(bundle: URL(fileURLWithPath: "/Applications/Herdr Companion Gateway.app"), parentWritable: true))
    }
}
