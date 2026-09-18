// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "HerdrMenu",
    platforms: [.macOS(.v13)],
    products: [.executable(name: "HerdrMenu", targets: ["HerdrMenu"])],
    targets: [.executableTarget(name: "HerdrMenu")]
)
