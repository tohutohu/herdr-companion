# Herdr Mobile

Mac 上の [Herdr](https://herdr.dev) で動いている Claude Code / Codex のセッションを、Android から確認・操作するためのアプリです。

- 完了・質問・承認待ちで Android に通知（タップで該当セッションへ）
- 通知受信時にログを先読みして Room に保存（開いた瞬間に読める）
- AskUserQuestion / Codex requestUserInput / 承認をネイティブ UI で回答
- 会話中の画像のインライン表示、Android からの画像送信
- メッセージ内のファイル参照をタップしてファイルを表示
- 非対応のダイアログは簡易ターミナル（`pane.read` + キー入力）で操作

```text
Android ──(Tailscale, HTTP + Bearer)──▶ herdr-mobile-gateway (Mac) ──▶ Herdr / Claude Code / Codex
```

設計の詳細は [docs/architecture.md](docs/architecture.md) を参照してください。
Gateway は DB を持たず、Herdr・Claude・Codex 自身のデータを都度読みます。

## リポジトリ構成

```text
gateway/   Go 製 Gateway（cmd/herdr-mobile-gateway, internal/...）
android/   Android アプリ（Kotlin / Jetpack Compose）
docs/      設計ドキュメント
```

## 前提

| 対象 | 必要なもの |
|---|---|
| Mac | Go 1.24+、Herdr 0.9+、Claude Code および/または Codex CLI、Tailscale |
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

任意項目: `fcmCredentialsFile`, `herdrSocket`, `claudeConfigDir`, `codexBinary`, `codexDaemonSocket`, `uploadDir`。

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

```bash
mkdir -p ~/Library/LaunchAgents ~/.local/state/herdr-mobile
sed "s#__HOME__#$HOME#g" gateway/deploy/com.herdr-mobile.gateway.plist > ~/Library/LaunchAgents/com.herdr-mobile.gateway.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.herdr-mobile.gateway.plist

# 再起動 / 停止
launchctl kickstart -k gui/$(id -u)/com.herdr-mobile.gateway
launchctl bootout gui/$(id -u)/com.herdr-mobile.gateway
```

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

Gateway は FCM HTTP v1 API に data-only / priority HIGH のメッセージを送り、アプリが通知表示とログ先読み（WorkManager）を行います。
`google-services.json` なしでもアプリはビルド・動作しますが、通知は無効になります。

## 4. Android アプリ

```bash
cd android
./gradlew assembleDebug                    # app/build/outputs/apk/debug/app-debug.apk
./gradlew installDebug                     # 接続中の端末へインストール
```

JDK 17 を使ってください（例: `export JAVA_HOME=$(/usr/libexec/java_home -v 17)`）。
`local.properties` に `sdk.dir` がない場合は Android Studio で一度開くか手動で作成します。

初回起動時に Gateway URL と認証トークンを入力し、「Test connection」→「Save」。
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
