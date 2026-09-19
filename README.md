# Herdr Mobile

Mac 上の [Herdr](https://herdr.dev) で動いている Claude Code / Codex / OpenCode（v1・公式 v2）のセッションを、Android から確認・操作するためのアプリです。

- 完了・質問・承認待ちで Android に通知（タップで該当セッションへ）
- 通知受信時にログを先読みして Room に保存（開いた瞬間に読める）
- AskUserQuestion / Codex requestUserInput / 承認をネイティブ UI で回答
- 会話中の画像のインライン表示、Android からの画像・ファイル送信
- メッセージ内のファイル参照をタップしてファイルを表示
- 非対応のダイアログは簡易ターミナル（`pane.read` + キー入力）で操作
- アプリから新しいセッションを起動（Claude Code / Codex / OpenCode、フォルダの選択・新規作成）

```text
Android ──(Tailscale, HTTP + Bearer)──▶ herdr-mobile-gateway (Mac) ──▶ Herdr / Claude Code / Codex
```

設計の詳細は [docs/architecture.md](docs/architecture.md) を参照してください。
Gateway は独自の会話 DB を持たず、Herdr と各エージェント自身のデータを都度読みます。

## Macのメニューバーアプリから始める

[macos/README.md](macos/README.md)にDMGの作成・導入手順があります。メニューバーからGatewayを起動し、Androidの「Scan Mac QR」で接続できます。
Firebase秘密鍵・Jevキーはどちらも任意です。未設定でも閲覧・送信・承認・セッション起動を利用できます。
通知を使う場合は、Mac画面でサービスアカウントJSONと`google-services.json`を取り込んでからペアリングしてください。Androidの再ビルドは不要です。
以下は従来のCLIによるセットアップ手順です。

## リポジトリ構成

```text
gateway/   Go 製 Gateway（cmd/herdr-mobile-gateway, internal/...）
android/   Android アプリ（Kotlin / Jetpack Compose）
macos/     メニューバーアプリ（SwiftUI）とDMGビルド
docs/      設計ドキュメント
```

## 前提

| 対象 | 必要なもの |
|---|---|
| Mac | Go 1.24+、Herdr 0.9+、Claude Code および/または Codex CLI、Tailscale |
| 残量表示 | CodexBar（`brew install --cask codexbar`、任意） |
| Android | Android 10 (API 29) 以上、Tailscale アプリ（同じ tailnet にログイン） |
| ビルド | JDK 17、Android SDK（compileSdk 37）|
| 通知 | Firebase プロジェクト（無料枠で可） |

## 1. Herdr のセットアップ

Gateway はペインごとの「どのセッションが動いているか」を Herdr の公式インテグレーションから取得します。**必須**です。

```bash
herdr integration install claude
herdr integration install codex
herdr integration status   # claude / codex が installed になっていること
```

インストール後に起動した Claude Code / Codex から認識されます（既存セッションは一度再起動してください）。

### Codex の構造化操作（推奨）

Codex の承認・requestUserInput・メッセージ送信を構造化 API で行うには、Codex TUI を共有 app-server daemon に接続して起動します。

```bash
codex app-server daemon start     # 共有 daemon（~/.codex/app-server-control/app-server-control.sock）
codex --remote unix://            # Herdr のペインで Codex をこの形で起動
```

daemon を使わない通常の `codex` でも履歴の閲覧とメッセージ送信（ペイン経由）はできますが、承認・質問への回答はターミナル画面からの操作になります。

## 2. Gateway のセットアップ

```bash
cd gateway
go build -o ~/.local/bin/herdr-mobile-gateway ./cmd/herdr-mobile-gateway
herdr-mobile-gateway token          # 初回実行で ~/.config/herdr-mobile/config.json と認証トークンを生成
```

`~/.config/herdr-mobile/config.json`（自動生成、パーミッション 600）:

```json
{
  "listen": ":8765",
  "authToken": "…",
  "devices": [],
  "offlineSessionDays": 3
}
```

任意項目: `fcmCredentialsFile`, `herdrSocket`, `claudeConfigDir`, `codexBinary`, `codexDaemonSocket`, `uploadDir`, `workspaceRoots`, `usageCommand`, `usageRefreshMinutes`。

`workspaceRoots`（既定: `~/workspace`、なければホーム）は、アプリからフォルダを選択・作成してセッションを起動できる範囲です。

### サブスクの残量表示

Claude / Codex のプラン上限の消費率をアプリの一覧画面に表示します。Claude Code も Codex も残量を CLI から出せないため、両方のダッシュボードを見に行く CodexBar の CLI を使います。Codex が Luna Reserve（`gpt-reserve`）を返すアカウントでは、その別枠も Limits に表示します。

```bash
brew install --cask codexbar     # /opt/homebrew/bin/codexbar が入る
herdr-mobile-gateway usage       # 取得できるか確認
```

Gateway は既定で 5 分ごとに読み直してキャッシュし、アプリはそれを表示します（カードの更新ボタンで即時再取得）。間隔は `usageRefreshMinutes`、無効化は `usageCommand: "off"` です。CodexBar が入っていなければ機能が無効になるだけで、他の動作には影響しません。

### アプリからのセッション起動

一覧画面の「New session」で、Claude Code / Codex、作業フォルダ（`workspaceRoots` 配下で選択または新規作成）、最初のプロンプトを指定して起動します。
Gateway は Herdr に新しいワークスペースを作り、`agent.start` でエージェントを起動します。
エージェントがフォルダ信頼確認ダイアログを出したときだけ、アプリが信頼するか確認します。「Trust」を選ぶと Gateway がダイアログに回答して起動を続け、「Cancel」を選ぶと作成したワークスペースを閉じます。
Codex の共有 daemon が動いていれば `codex --remote unix://…` で起動します。

Codex では、Herdr インテグレーションのフックを一度「信頼」する必要があります（Codex 起動時に表示される Hooks の確認画面で `t`）。

### 開始前のフォルダ確認（Jev、任意）

Gateway の環境変数 `TYPESAFE_API_KEY`、または `~/.config/herdr-mobile/config.json` の
`jevApiKey` に TypeSafe のAPIキーを設定し、Gatewayを再起動すると有効になります。
環境変数が優先されます。launchd ではターミナルの環境変数を継承しないため、
config.json での設定が簡単です。キーはAndroidへ送られません。

新規セッションで「Start」を押すと、入力した指示と選択フォルダを照合します。
明確な不一致だけ「Change folder / Start anyway」を表示します。
未設定・判定不能・通信失敗ではそのまま開始し、指示が空の場合はチェックしません。
旧Gatewayとの組み合わせでも開始できます。

**有効化すると以下の抜粋をTypeSafe APIへ送信します。**

- 入力した指示とフォルダ名、直下の隠しファイル以外の名前（最大60件）
- 直下のREADME.md、AGENTS.mdと代表的な構成ファイル（各先頭4KiBまで）
- 同じ実ディレクトリの直近30日・最大3セッションから、最近の指示3件と最後の報告（各1,000文字まで）

履歴はClaude／Codexの既存の最近のセッション取得範囲内で探します。
別ディレクトリの会話、画像、ツール結果、ソース本文全体は送信対象にしません。
ただし、指示や報告・READMEに含まれる情報はそのまま抜粋されるため、
外部送信できるプロジェクトで利用してください。判定用の本文やAPI応答はログに残しません。
6,000文字を超える指示は、一部だけで誤判定しないようチェックを省略します。
Jevの誤判定はあり得るので、開始の禁止やディレクトリの自動変更は行いません。

### 認証トークン

すべての `/v1/*` API は `Authorization: Bearer <token>` が必要です。Tailscale 内でも省略できません。

```bash
herdr-mobile-gateway token            # 表示
herdr-mobile-gateway token --rotate   # 再発行（アプリ側の設定も更新してください）
```

### Tailscale hostname

Gateway を public internet に公開する必要はありません。Mac と Android を同じ tailnet に入れ、アプリには MagicDNS 名か Tailscale IP を設定します。

```bash
tailscale status              # Mac のホスト名（例: my-mac）を確認
tailscale ip -4               # 例: 100.101.102.103
```

アプリの Gateway URL 例: `http://my-mac.<tailnet名>.ts.net:8765` または `http://100.101.102.103:8765`

Tailscale 以外から到達させたくない場合は、Tailscale IP だけで待ち受けます:

```bash
herdr-mobile-gateway serve --listen 100.101.102.103:8765
```

（通信は WireGuard で暗号化されるため、アプリは tailnet 上の平文 HTTP を許可しています。）

### launchd で常駐させる

Herdr サーバー（ヘッドレス）と Gateway をそれぞれ LaunchAgent にします。`herdr` TUI は起動中のサーバーにアタッチします。

```bash
mkdir -p ~/Library/LaunchAgents ~/.local/state/herdr-mobile
LISTEN=$(tailscale ip -4):8765
sed -e "s#__HOME__#$HOME#g" -e "s#__HERDR__#$(which herdr)#g" \
  gateway/deploy/com.herdr-mobile.herdr-server.plist > ~/Library/LaunchAgents/com.herdr-mobile.herdr-server.plist
sed -e "s#__HOME__#$HOME#g" -e "s#__LISTEN__#$LISTEN#g" \
  gateway/deploy/com.herdr-mobile.gateway.plist > ~/Library/LaunchAgents/com.herdr-mobile.gateway.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.herdr-mobile.herdr-server.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.herdr-mobile.gateway.plist

# 状態 / 再起動 / 停止
launchctl list | grep herdr-mobile
launchctl kickstart -k gui/$(id -u)/com.herdr-mobile.gateway
launchctl bootout gui/$(id -u)/com.herdr-mobile.gateway
```

Herdr サーバーの plist は PATH を最小にしています。ペインのログインシェルが普段のターミナルと同じ順序で PATH を組み立てるためです（長い PATH を渡すと、古い Homebrew 版 `claude` などが先に見つかることがあります）。
Herdr サーバーはクラッシュ時のみ自動再起動し、`herdr server stop` による停止は尊重します。

ログ: `~/.local/state/herdr-mobile/gateway.log`（JSON、10MB × 3 世代でローテーション）

## 3. Firebase（通知）のセットアップ

秘密情報（`google-services.json`、サービスアカウント JSON）は **リポジトリにコミットしないでください**（`.gitignore` 済み）。

1. [Firebase コンソール](https://console.firebase.google.com/) で新規プロジェクトを作成
2. Android アプリを追加（パッケージ名 `com.tohutohu.herdrmobile`）し、`google-services.json` を `android/app/google-services.json` に置く
3. プロジェクトの設定 → サービスアカウント → 「新しい秘密鍵を生成」で JSON をダウンロードし、Mac に置く
   ```bash
   mv ~/Downloads/<project>-firebase-adminsdk-*.json ~/.config/herdr-mobile/firebase-service-account.json
   chmod 600 ~/.config/herdr-mobile/firebase-service-account.json
   ```
4. Gateway に場所を渡す（どれか）
   - 環境変数 `HERDR_MOBILE_FCM_CREDENTIALS`（launchd の plist で設定済み）
   - `config.json` の `fcmCredentialsFile`
   - `GOOGLE_APPLICATION_CREDENTIALS`
5. アプリで Gateway 設定を保存すると端末が登録されます。確認:
   ```bash
   herdr-mobile-gateway devices
   herdr-mobile-gateway notify-test
   ```

CLI で行う場合（firebase CLI と gcloud にログイン済みのとき）:

```bash
PROJECT=herdr-client-android
firebase apps:create android "Herdr Mobile" --package-name com.tohutohu.herdrmobile --project $PROJECT
firebase apps:sdkconfig ANDROID <表示された App ID> --project $PROJECT --out android/app/google-services.json
gcloud iam service-accounts keys create ~/.config/herdr-mobile/firebase-service-account.json \
  --iam-account firebase-adminsdk-fbsvc@$PROJECT.iam.gserviceaccount.com --project $PROJECT
```

Gateway は FCM HTTP v1 API に data-only / priority HIGH のメッセージを送り、アプリが通知表示とログ先読み（WorkManager）を行います。
共通配布APKはFirebase設定を内蔵せず、MacとのQRペアリングで取り込みます。未設定でも通知以外は動作します。
上記の従来方式で自分専用APKに設定を内蔵する場合だけ、ビルド時に`-PbundleFirebase=true`を指定してください。

## 4. Android アプリ

```bash
cd android
./gradlew --console=plain assembleRelease   # app/build/outputs/apk/release/app-release.apk
./gradlew installRelease                   # 接続中の端末へインストール
```

通常のAPKビルドは **release版（R8・リソース圧縮ともに有効）** を使います。
現在はインストール用にdebug署名キーで署名していますが、ビルド自体は最適化済みのrelease版です。
debug版が明示的に必要な場合だけ `assembleDebug` / `installDebug` を使ってください。

JDK 17 を使ってください（例: `export JAVA_HOME=$(/usr/libexec/java_home -v 17)`）。
`local.properties` に `sdk.dir` がない場合は Android Studio で一度開くか手動で作成します。

初回起動時は「Scan Mac QR」でMacのQRを読み取るか、Gateway URLと認証トークンを入力し「Test connection」→「Save」。
スマホで MagicDNS 名が引けない場合は Tailscale IP（`http://100.x.y.z:8765`）を使ってください。

デバッグビルドは adb から設定を渡せます（日本語 IME で `adb shell input text` が変換されるのを避けるため）。受け口は adb にしか送れない receiver です:

```bash
adb shell am broadcast -n com.tohutohu.herdrmobile/.DebugConfigReceiver \
  --es gateway_url "http://100.x.y.z:8765" --es token "$(herdr-mobile-gateway token)"
adb shell am start -n com.tohutohu.herdrmobile/.MainActivity
```
Android 13 以降は通知の許可を求められます。

## 開発

```bash
# Gateway
cd gateway
go run ./cmd/herdr-mobile-gateway --debug --log-file ""   # 開発起動（:8765）
go test ./...
go test ./internal/providers/... -update                 # fixture の golden を更新
go vet ./...

# 別の Herdr セッションに向ける
HERDR_SESSION=mytest go run ./cmd/herdr-mobile-gateway

# 処理できなかったデータの再解析
go run ./cmd/herdr-mobile-gateway debug replay ~/.local/state/herdr-mobile/errors/2026-09-17.jsonl

# API を叩く
TOKEN=$(go run ./cmd/herdr-mobile-gateway token)
curl -H "Authorization: Bearer $TOKEN" localhost:8765/v1/sessions

# Android
cd android
./gradlew testDebugUnitTest
./gradlew assembleDebug
```

### テストデータ

`gateway/testdata/{claude,codex}/` に実データ由来の fixture（パスは匿名化済み）と、変換結果の golden JSON があります。
Claude / Codex の仕様変更で dead-letter に記録されたデータは、raw を fixture に追加して adapter を更新してください。

## 制約・既知の事項

- Claude Code の AskUserQuestion / 承認への回答は、構造化 API がないためペインへのキー入力に変換しています（Claude Code 2.1 系で確認）。UI が変わった場合はアプリの「Terminal」から操作できます。
- Codex の構造化回答は共有 daemon（`codex --remote unix://`）で動くセッションのみです。
- セッションの identity は Herdr インテグレーションが報告する session id です。インテグレーション未導入のペインは一覧に出ません。
- 通知の重複防止はメモリ上のみで、Gateway 再起動直後の状態は通知しません。

## OpenCode（v1 / 公式 v2）

アプリのエージェント選択で **OpenCode** を選びます。Herdr の PATH にある
`opencode` を起動し、インストール済みのバージョンを使います。
[公式 v2](https://opencode.ai/v2/docs) は v2.0.9 で API 接続を確認しています。
Herdr のセッション識別・状態通知には、OpenCode を一度起動した後で
`herdr integration install opencode` を実行してください。既に導入済みの場合も
v2 の TUI 連携を含む最新の Herdr integration に更新します。

- 会話は `~/.local/share/opencode/opencode.db` を読み取り専用で参照します。
  v1 の `session` / `message` / `part` と v2 の `session_v2` / `session_message`
  を読み分けます。XDG_DATA_HOME と OPENCODE_DB にも対応します。
- v2 の共有サービスは `~/.local/state/opencode/service.json` から自動検出します。
  送信、画像・ファイル添付、承認、通常の選択式・自由記述フォームに対応します。
  条件付きフォームや数値入力など、アプリで表現できないものは端末で回答します。
- v1 は通常のテキスト送信を Herdr 経由で行います。承認・質問への構造化回答や
  画像添付には、TUI と同じ v1 サーバーへの接続設定が必要です。
- v2 でモデルを指定する場合は共有サービスが必要です。モデル一覧を取得すると
  OpenCode CLI が共有サービスを起動します。v2 では API でモデルを設定した
  セッションを作り、`--session` で TUI を開きます。

独自の保存先や、既存の v1 サーバーを使う場合は Gateway の private config に
`opencode` を追加します（以下は例。ほかの設定は保持してください）。

```json
{
  "opencode": {
    "database": "/absolute/path/opencode.db",
    "binary": "opencode",
    "serverUrl": "http://127.0.0.1:4096",
    "serverVersion": 1,
    "username": "opencode",
    "password": "your-server-password"
  }
}
```

`serverUrl` を省略すると公式 v2 の共有サービスを検出します。v2 の接続先を明示する
場合は `serverVersion: 2` を指定します。`stateDir` でサービス登録ファイルの
ディレクトリも変更できます。`binary` はモデル一覧・バージョン確認に使う実行ファイルで、
Herdr が起動する `opencode` と同じバージョンを指定します。
JSON ファイルに保存していた古い v1（SQLite 移行前）は対象外です。

### 設定画面から OpenCode を更新

「Agent updates on Mac」に OpenCode のバージョンと「Check and update」を表示します。
[公式の `upgrade` コマンド](https://opencode.ai/v2/docs/cli/commands#upgrade)を実行し、
完了後にバージョンを再取得します。v1 と公式 v2 のどちらも対象です。
`opencode.binary` を指定している場合は、その実行ファイルを更新します。
インストール方式を CLI が判定できない場合は、更新出力とエラーを画面に表示します。
更新中に画面を離れても処理は続き、同じエージェントの重複更新は実行しません。
