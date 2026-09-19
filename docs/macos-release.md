# Herdr Desktop macOS release

このリポジトリのmacOS配布は、UIとGatewayを別アプリ・別DMGとして扱います。

| 配布物 | 内容 | Bundle ID |
| --- | --- | --- |
| `Herdr.app` / `Herdr-x.y.z.dmg` | Compose Multiplatform UI、同梱JRE | `com.tohutohu.herdrmobile.desktop` |
| `Herdr Mobile.app` / `Herdr-Mobile-x.y.z.dmg` | SwiftUI Gateway Manager、同梱Go Gateway | `com.tohutohu.herdrmobile.mac` |

`Herdr.app`はGatewayを内包せず、既存の`Herdr Mobile.app`が起動したGatewayへ接続します。Gatewayが不要な利用者はUI DMGだけ、DesktopからMac上のセッションへ接続する利用者は2つのDMGを個別にインストールできます。

## 固定しているビルド条件

- Kotlin `2.4.20`
- Compose Multiplatform `1.12.0`
- Gradle wrapper `9.6.0`
- JDK `17`
- minimum macOS `13.0`
- Apple Silicon `arm64` only
- UI build version source: [`version.properties`](../version.properties)

Compose Multiplatform 1.12.0のmacOSターゲットがarm64のみのため、Intel版やUniversal Binaryは生成しません。Composeの互換性情報は[Kotlin公式Compatibility and versioning](https://kotlinlang.org/docs/multiplatform/compose-compatibility-and-versioning.html)を参照してください。

`version.properties`の`version`は`CFBundleShortVersionString`、`build`は`CFBundleVersion`、Composeの`packageVersion`、Androidの`versionName`/`versionCode`に共有されます。macOSのjpackageは先頭が0のバージョンを受け付けないため、production releaseは`1.0.0`以降を使います。

```properties
version=1.0.0
build=1
```

## ローカルビルド

必要なものはJDK 17、Xcode Command Line Tools、Go 1.24以上、arm64 macOSです。アイコンを再生成するときだけ`librsvg`（`brew install librsvg`）も必要です。

通常のrelease成果物は次の1コマンドで生成します。

```bash
./scripts/build-macos-release.sh
```

生成物:

```text
dist/Herdr.app
dist/Herdr-1.0.0.dmg
dist/Herdr-1.0.0.dmg.sha256
dist/Herdr Mobile.app
dist/Herdr-Mobile-1.0.0.dmg
dist/Herdr-Mobile-1.0.0.dmg.sha256
```

UIはComposeの`createReleaseDistributable`でJRE同梱のoptimized app imageを生成します。最終UI DMGには`Herdr.app`と`Applications`ショートカットだけを入れ、Gateway DMGには`Herdr Mobile.app`と同じショートカットだけを入れます。

### Composeの公式タスク

`android`ディレクトリで、実際に利用できるタスクは次のとおりです。

```bash
cd android
export JAVA_HOME="$HOME/Library/Java/JavaVirtualMachines/jbr-17.0.14/Contents/Home"

./gradlew :desktopApp:createDistributable
./gradlew :desktopApp:createReleaseDistributable
./gradlew :desktopApp:runReleaseDistributable
./gradlew :desktopApp:packageDmg
./gradlew :desktopApp:packageReleaseDmg
./gradlew :desktopApp:notarizeDmg
./gradlew :desktopApp:notarizeReleaseDmg
```

`packageReleaseDmg`はCompose plugin標準のUI単体DMGを生成します。このCompose versionの標準DMGには`Applications`ショートカットが含まれないため、最終配布では上記release app imageを`hdiutil`の小さなwrapperでDMG化しています。アプリ本体のJRE、jlink module、ProGuard、metadata、Compose署名設定は公式pluginに任せています。

最小JDK moduleは`java.instrument`、`java.prefs`、`jdk.unsupported`です。必要なmoduleは`./gradlew :desktopApp:suggestRuntimeModules`でも確認できます。`includeAllModules`でruntimeを肥大化させていません。

## アイコン

ブランド用の`macos/assets/Herdr.svg`をsourceとし、macOS上で次を実行すると再現可能に`Herdr.icns`を更新できます。

```bash
macos/scripts/generate-icon.sh
```

生成した`macos/assets/Herdr.icns`はCompose UIとSwiftUI Gateway Managerの両方で使用します。

## Developer ID署名

証明書や秘密鍵はリポジトリへ置きません。ローカルではKeychainにDeveloper ID Application証明書を入れ、環境変数で参照します。

```bash
export MACOS_SIGNING_IDENTITY='Developer ID Application: …'
export MACOS_SIGNING_KEYCHAIN="$HOME/Library/Keychains/herdr-build.keychain-db"
./scripts/build-macos-release.sh
```

Compose側は公式のmacOS signing DSLを使い、同梱JREとnested codeを含めてjpackage/codesignが処理します。Gateway側はGo helperを先に署名し、その後SwiftUI appを署名します。App Sandbox用のentitlementは追加していません。Compose 1.12.0がJVM runtime用に生成する標準entitlement（JIT、unsigned executable memory、library validation無効化）だけを利用します。

Composeタスクを直接実行する場合は公式Gradle propertyも使用できます。

```bash
./gradlew :desktopApp:packageReleaseDmg \
  -Pcompose.desktop.mac.sign=true \
  -Pcompose.desktop.mac.signing.identity='Developer ID Application: …' \
  -Pcompose.desktop.mac.signing.keychain="$MACOS_SIGNING_KEYCHAIN"
```

## Notarization

実装している認証方式はApple ID + app-specific password + Team IDです。値は環境変数またはCI secretsからだけ渡します。

```bash
export APPLE_ID='…'
export APPLE_APP_SPECIFIC_PASSWORD='…'
export APPLE_TEAM_ID='…'
./scripts/build-macos-release.sh
```

最終DMGはUIとGatewayで別々にAppleへ提出し、それぞれへstapleします。UIの最終DMGは`Applications`ショートカットを追加した成果物なので、Compose pluginのUI-only `notarizeReleaseDmg`ではなく、Apple公式の`xcrun notarytool`/`xcrun stapler`をrelease wrapperが使用します。Composeの公式notarization DSLと`notarizeReleaseDmg`自体も利用可能な状態です。

認証情報が揃っていないローカル環境では、ad-hoc署名のapp/DMGまでは生成できます。その成果物についてGatekeeper合格とは報告しません。

## 検証

release wrapperは各DMGについて次を実行します。

- `codesign --verify --deep --strict`
- Developer ID設定時の`Developer ID Application` authority確認
- `hdiutil verify`
- read-only mount、アプリ名、`Applications` shortcut確認
- `.env`、`local.properties`、Firebase service account、証明書・秘密鍵の混入確認
- notarization設定時の`xcrun stapler validate`と`spctl --assess`

個別に再検証する場合:

```bash
REQUIRE_SIGNED=1 EXPECT_NOTARIZED=1 \
  ./scripts/verify-macos-release.sh \
  dist/Herdr.app dist/Herdr-1.0.0.dmg

REQUIRE_SIGNED=1 EXPECT_NOTARIZED=1 \
  ./scripts/verify-macos-release.sh \
  'dist/Herdr Mobile.app' dist/Herdr-Mobile-1.0.0.dmg
```

ローカルunsigned buildの確認には`ALLOW_UNSIGNED=1`を指定できます。`spctl`が通らないことはunsigned buildでは想定内であり、Gatekeeper回避をインストール手順にはしません。

## パッケージからの起動確認

IDEや`Gradle run`ではなく、生成したbundleを直接起動します。

```bash
open dist/Herdr.app
open 'dist/Herdr Mobile.app'
```

UIだけを先に入れた場合、Gateway設定が存在しないとUIは設定案内を表示します。これはGatewayをUIへ隠れて同梱しない設計上の明示的な状態です。両方を利用する場合は、先に`Herdr Mobile.app`を起動してGatewayを起動し、既存の`~/.config/herdr-mobile/desktop/config.json`を利用します。ログと状態は`~/.local/state/herdr-mobile/desktop/`です。

## インストール手順

UIだけの場合:

1. `Herdr-x.y.z.dmg`を開く
2. `Herdr.app`を`Applications`へドラッグする
3. `Herdr`を起動する

Gatewayも使う場合は、別途`Herdr-Mobile-x.y.z.dmg`を開いて`Herdr Mobile.app`を`Applications`へドラッグし、先に起動します。Developer ID署名とnotarization済みのreleaseでは、右クリックの「開く」や`xattr -d com.apple.quarantine`は通常手順にしません。

## GitHub Actions / GitHub Release

[`.github/workflows/macos-release.yml`](../.github/workflows/macos-release.yml)は`v*` tag pushまたはmanual dispatchで動きます。`macos-14` arm64 runner上で、JDK 17、Gradle cache、Go 1.24、Android/shared regression、Gateway test/vet、temporary keychain、2つのrelease DMG生成、verification、artifact uploadを順に実行します。tag push時は次の4ファイルをGitHub Releaseへ添付します。

```text
Herdr-x.y.z.dmg
Herdr-x.y.z.dmg.sha256
Herdr-Mobile-x.y.z.dmg
Herdr-Mobile-x.y.z.dmg.sha256
```

CIへ設定するsecret名:

- `MACOS_CERTIFICATE_P12_BASE64`
- `MACOS_CERTIFICATE_PASSWORD`
- `APPLE_ID`
- `APPLE_APP_SPECIFIC_PASSWORD`
- `APPLE_TEAM_ID`

証明書がない場合もCIはunsigned artifact生成まで進みます。notarization用3 secretの一部だけを設定する構成は拒否します。

## Version tagの手順

1. `version.properties`の`version`を更新する
2. 必要なら`build`を更新する（CIでは`GITHUB_RUN_NUMBER`を優先）
3. Android、Desktop、Gatewayの回帰テストを実行する
4. `v1.0.0`のようにversionと一致するtagをpushする

```bash
git tag v1.0.0
git push origin v1.0.0
```

tag名と`version.properties`が一致しないreleaseはwrapperが停止します。

## Known limitations / 次フェーズ

今回の配布では、次を実装していません。

- IntelまたはUniversal Binary
- UIからのGateway lifecycle管理、single-instance統合
- launch-at-loginの新設（既存Gateway Managerの機能は維持）
- Sparkle等のautomatic updater、release channel、rollback

次フェーズでは、現行の別アプリ構成を保ったまま、Gateway Managerを所有者として次の順に拡張するのが安全です。

1. UIがGateway Managerの状態をIPC/localhost health endpointで参照するGateway lifecycle integration
2. Gateway Managerをsingle-instance coordinatorにし、UIは既存Gatewayへ再接続する
3. launch-at-loginはGateway Managerだけに限定する
4. UIとGatewayを同じrelease manifestで配布し、stable/beta等のrelease channelを分ける
5. Developer ID/notarizationに加えて更新payloadへ独立したupdate signingを付ける
6. staged update、起動health check、旧bundle保持によるrollbackを実装する

automatic updaterは今回のrelease pipelineへ追加していません。
