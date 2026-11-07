# Herdr Companion

英語版: [README.md](README.md)

Mac 上の Claude Code / Codex のセッションを、Android（と Compose Desktop UI、他のエージェント向けの `hc` CLI）から起動・確認・操作するためのアプリです。Mac で AI エージェントを走らせたまま、スマホから進捗を確認したり、質問に答えたり、モデルを切り替えたり、次の指示を送ったりできます。

Claude Code（公式 Claude Agent SDK 経由）と Codex（Gateway 専用の app-server 経由）は Gateway が直接動かすので、**Herdr は必要ありません**。2.0 から Gateway は [Herdr](https://herdr.dev) と一切連携せず、OpenCode と Devin にも対応しなくなりました。Herdr を単独で使うことや、エージェントが自分のツールから `herdr` を呼ぶことは引き続き自由です。1.x からの移行は [1.x からの移行](#1x-からの移行) を参照してください。

## このアプリの良さ

特に便利なのは、アプリをずっと画面に出しておかなくてもセッションを追えることです。

- エージェントの完了・質問・承認待ち・エラーを Android の通知で知らせる
- 通知を受けた時点で会話をバックグラウンド取得し、端末の Room キャッシュへ保存する
- 通知をタップすると、該当セッションの最新ログをすぐ開ける（ネットワーク状況によっては再試行）
- 通知のインライン返信から、通常のメッセージをそのまま送れる
- AskUserQuestion、Codex の `requestUserInput`、承認操作はネイティブ UI で回答できる
- 画像・ファイルの送信、会話中の画像表示、メッセージ内のファイル参照にも対応
- 添付は500 MiBまでストリーミング送信（Android・Desktop・更新済みGateway）
- アプリから新しいセッションを起動し、作業フォルダやモデルも選べる
- 動作中のセッションのモデルと推論強度を、履歴を保ったまま変えられる
- **常駐タスク**: 「毎朝このリポジトリを確認して、対応が必要なら知らせて」のような継続して任せる指示を、トップレベルの「常駐」タブで管理できる。予定（毎日・毎週・cron・1回、IANA timezone）や手動で Gateway が実行し、前回の状態を覚え、必要なときだけ通知する。追加指示・判断待ちへの回答・実行履歴もタスクごとに持つ。実行は専用の Claude Code / Codex プロセスで行い、セッション一覧には混ざらない。詳しくは [docs/resident.md](docs/resident.md)

通知から画面を開くまでの流れは次のとおりです。

```text
Mac上のGatewayが自分の動かすセッションを監視
        │ FCM通知
        ▼
Androidがバックグラウンドで会話を取得 → Roomに保存 → 通知タップで即表示
```

Firebase のプロジェクト作成とサービスアカウント設定は、正直に言うとこのプロジェクトで一番面倒な部分です。ただし通知を使わない場合でも、閲覧・送信・承認・セッション起動は利用できます。通知を使う場合も、推奨のメニューバーアプリなら設定は一度だけで、共通 APK の再ビルドは必要ありません。

Gateway は Mac 上で動き、会話データをクラウドへ保存する独自 DB は持ちません。各エージェント自身のデータ（Claude Code の transcript、Codex のスレッド）を読み、Android 側に必要な範囲だけキャッシュします。

```text
Android / Desktop / hc ──(Tailscale推奨 / trusted LAN / HTTPS tunnel, HTTP(S) + Bearer)──▶ Herdr Companion Gateway (Mac)
    ├─▶ Claude Agent SDK サイドカー ──▶ Claude Code
    └─▶ codex app-server（stdio）   ──▶ Codex
```

設計の詳細は [docs/architecture.md](docs/architecture.md) を参照してください。

## 最短の導入手順

1. Mac に Claude Code（`claude` を起動し、サブスクで `/login`）と Codex を用意してログインする。Herdr は不要。
2. Mac で `Herdr Companion Gateway.app` を起動し、Gateway を起動する。
3. Android アプリの **Scan Mac QR** でメニューバーアプリのペアリング QR を読み取る。
4. 通知が必要なら、Firebase を設定してから QR を発行し直してペアリングする。

Gateway と Android の接続には、Mac と Android が同じ tailnet にいる Tailscale を推奨します。信頼できる LAN や HTTPS トンネルも利用できます。Mac のメニューバーアプリの使い方は [macos/README.md](macos/README.md)、DMG の作成・配布は [docs/macos-release.md](docs/macos-release.md) を参照してください。

Firebase の秘密鍵と Jev の API キーは任意です。どちらも未設定のままペアリングできます。

## リポジトリ構成

```text
gateway/   Go 製 Gateway（cmd/herdr-mobile-gateway, internal/...）
android/   Android アプリとCompose Desktop UI（Kotlin / Compose）
macos/     Gateway Manager（SwiftUI）とGateway用DMGビルド
docs/      設計・macOS releaseドキュメント
```

## 前提

| 対象 | 必要なもの |
|---|---|
| Mac | ログイン済みの Claude Code / Codex CLI、ネットワーク接続（Tailscale推奨） |
| Gateway のビルド | Go 1.27+、Bun 1.3+（Claude サイドカー用） |
| 残量表示 | CodexBar（`brew install --cask codexbar`、任意） |
| Android | Android 10 (API 29) 以上、Macへ到達できるネットワーク（Tailscale推奨） |
| ビルド | JDK 17、Android SDK（compileSdk 37）|
| 通知 | Firebase プロジェクト（無料枠で可） |

## 1. エージェント

Claude Code と Codex はインストールしてログインしておくだけで使えます。Gateway が直接起動します。Claude Code のセッションは既存のログインとサブスク枠を使い、従量課金の API には切り替えません（`claudeAllowApiKey` を設定しない限り `ANTHROPIC_API_KEY` を Claude Code に渡さず、API キー課金になるセッションは理由を示して開始しません）。CLI と同じ設定（CLAUDE.md、`.claude/`、Skills、Hooks、MCP サーバー、カスタムエージェント、Permission Rules）を読み込みます。Codex は Gateway が自分用に起動する `codex app-server` で動きます。履歴は Claude Code・Codex 自身のローカル保存のままです。

アプリの設定画面の「Agent updates on Mac」に Claude Code と Codex のバージョンを表示し、「Check and update」で `claude update` / `codex update` を実行します。

### 1.x からの移行

2.0 では Herdr のペインを前提にした機能と、OpenCode・Devin への対応を削除しました。

- Herdr のペインで動いていた Claude Code / Codex のセッションは停止中（offline）と表示されます。「Resume」（または `hc resume`）で同じ ID のまま Gateway が再開します。先にペインのエージェントを終了してください（2 つのプロセスが同じ履歴に書き込むため）。OpenCode / Devin のセッションは一覧に出なくなります。
- 設定の `herdrSocket`、`opencode`、`devin`、`codexDaemonSocket` は無視されます。削除してかまいません。
- Herdr Companion には Herdr サーバー、その LaunchAgent（`com.herdr-mobile.herdr-server`）、`herdr integration install`、Codex の共有 app-server daemon が不要になりました。Herdr を単独で使う場合だけ残してください。
- アプリの「Terminal」画面、Mac UI から Herdr のペインを開く機能、`hc term` / `hc keys` / `hc migrate` / `--backend` はなくなりました。
- 1.x が Herdr での起動用に作った worktree も、アーカイブ時にほかと同じく git で削除します。

## 2. Gateway のセットアップ

### メニューバー版（推奨）

配布 DMG を使う場合は、`Herdr Companion Gateway.app` を Applications に入れて起動し、メニューバーのアイコンから Gateway を起動します。表示された接続先を確認して「ペアリングQRを表示」→ Android の「Scan Mac QR」で接続してください。

`Herdr Companion.app`（Compose Desktop UI）も利用する場合は、UI 用 DMG を別途インストールします。Gateway Manager と UI は別アプリです。Firebase の取り込み、ログイン時起動、Gateway の start/stop もメニューバー版で管理できます。詳しくは [macos/README.md](macos/README.md) を参照してください。

同じデスクトップ UI は Linux でも動きます。各 GitHub Release に `Herdr-Companion-Linux-x.y.z-amd64.deb` と `-arm64.deb` があり、`sudo apt install ./Herdr-Companion-Linux-*.deb` でインストールできます。自分でビルドする場合、jpackage は実行中の OS 向けにしかパッケージを作れないため、`.deb` は Linux 上でビルドします（JDK 17 と、Gradle ビルドに Android アプリが含まれるため Android SDK も必要）。jpackage はビルド時にインストールされているライブラリからパッケージの依存関係を決めるので、ビルド前に `libegl1` を入れてください。

```bash
cd android
./gradlew :desktopApp:packageReleaseDeb   # desktopApp/build/compose/binaries/main-release/deb/herdr-companion_*.deb
sudo apt install ./desktopApp/build/compose/binaries/main-release/deb/herdr-companion_*.deb
```

メニューバーアプリのないマシンでは **Settings** を開き、Gateway の接続先（例: `http://100.x.y.z:8766`）と、Mac で `herdr-mobile-gateway token` が表示したトークンを入力します。通知は `notify-send` を使います。Linux 版は自動アップデートしません。

### CLI / launchd 版

配布アプリを使わず、Gateway をソースから起動する場合は次の手順です。

```bash
cd gateway
go build -o ~/.local/bin/herdr-mobile-gateway ./cmd/herdr-mobile-gateway
~/.local/bin/herdr-mobile-gateway token  # 初回実行で設定と認証トークンを生成
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

任意項目: `fcmCredentialsFile`, `claudeConfigDir`, `claudeBinary`（直接管理で使う Claude Code CLI。既定は PATH の `claude`、なければ `~/.local/bin/claude`）, `claudeSidecar`（既定は Gateway と同じ場所の `herdr-claude-sidecar`、なければ PATH）, `claudeAllowApiKey`, `codexBinary`, `uploadDir`, `workspaceRoots`, `usageCommand`, `usageRefreshMinutes`, `historyCacheMB`（エージェントごとに会話履歴のデコード結果を保持するメモリ上限。既定 2048 MiB）。

`workspaceRoots`（既定: `~/workspace`、なければホーム）は、アプリからフォルダを選択・作成してセッションを起動できる範囲です。

### サブスクの残量表示

Claude / Codex のプラン上限の消費率をアプリの一覧画面に表示します。Claude Code も Codex も残量を CLI から出せないため、両方のダッシュボードを見に行く CodexBar の CLI を使います。Codex が Luna Reserve（`gpt-reserve`）を返すアカウントでは、その別枠も Limits に表示します。DeepSeek Harness 用に OpenCode Go の API キーを保存していれば、同じキーで OpenCode Go の 5 時間・週・月の枠も表示します。

```bash
brew install --cask codexbar     # /opt/homebrew/bin/codexbar が入る
herdr-mobile-gateway usage       # 取得できるか確認
```

Gateway は既定で 5 分ごとに読み直してキャッシュし、アプリはそれを表示します（カードの更新ボタンで即時再取得）。間隔は `usageRefreshMinutes`、無効化は `usageCommand: "off"` です。CodexBar が入っていなければ機能が無効になるだけで、他の動作には影響しません。

### アプリからのセッション起動

一覧画面の「New session」で、Claude Code / Codex、作業フォルダ（`workspaceRoots` 配下で選択または新規作成）、最初のプロンプトを指定して起動します。

Gateway がエージェントを直接起動し、セッション ID はすぐに決まります。そのエージェントが一度も信頼していないフォルダでは、起動前にアプリが確認します（プロジェクトの Hooks や MCP サーバーがコードを実行するため）。worktree を指定すると Gateway が git で `worktree/<名前>` ブランチの worktree を作り、アーカイブ時に未コミット・未追跡の変更がなければ削除します（ブランチは残します）。

動作中のセッションは、詳細画面や `hc` からモデル・推論強度・モードの変更と、実行中ターンの中断ができます。Claude Code は次のモデル呼び出しから（実行中のターン内のこともある）、Codex は次のターンから反映され、それまでアプリは「適用待ち」と表示します。エージェントが提供しない値は理由を示して拒否し、別の値に置き換えません。

Gateway の停止中に作業していたターンはそこで途切れ（エージェントのプロセスは Gateway と一緒に終了）、再起動後はそのセッションを failed と表示します。何も再実行しません。履歴はそのまま読め、次のメッセージで同じ ID のまま新しいプロセスで再開します。

ターミナルで起動したものなど Gateway が動かしていないセッションは、`offlineSessionDays` 以内に更新されていれば停止中（offline）として一覧に出ます。「Resume」で同じ ID のまま Gateway が再開します。Gateway が動かしているセッションは、プロセスが止まっていても offline にはならず、次のメッセージで再開します。終わったターンは、セッションを開くまで未読として表示します。

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

すべての `/v1/*` API は、接続経路にかかわらず `Authorization: Bearer <token>` が必要です。

```bash
herdr-mobile-gateway token            # 表示
herdr-mobile-gateway token --rotate   # 再発行（アプリ側の設定も更新してください）
```

### 接続方法（Tailscale推奨）

Tailscaleを使うと、MacとAndroidが別のネットワークにいてもGatewayを公開インターネットへ直接さらさず接続できます。両方を同じtailnetに入れ、アプリにはMagicDNS名かTailscale IPを設定してください。

```bash
tailscale status              # Mac のホスト名（例: my-mac）を確認
tailscale ip -4               # 例: 100.101.102.103
```

アプリの Gateway URL 例: `http://my-mac.<tailnet名>.ts.net:8765` または `http://100.101.102.103:8765`

Tailscaleを使わない場合は、MacとAndroidを同じ信頼できる家庭・社内LANに接続し、MacのIPをGateway URLに設定できます。Macのメニューバーアプリは、Tailscaleが見つからない場合にローカルIPv4も検出します。

Cloudflare Tunnelなどを使う場合は、Mac上のGatewayを通常どおり起動し、AndroidのSettingsでGateway URLにTunnelの`https://...` URLを入力します。認証トークンは同じGatewayのものを使います。公開経路ではHTTPSのTunnelを使い、平文HTTPをインターネットへ直接公開しないでください。

CLIでTailscale IPだけに待ち受けさせたい場合は次のようにします:

```bash
herdr-mobile-gateway serve --listen 100.101.102.103:8765
```

（Tailscale上のHTTPはWireGuardで暗号化されます。LANでHTTPを使う場合も、信頼できるネットワーク内に限定してください。インターネットへ直接公開する場合はHTTPSと追加のアクセス制御を用意してください。）

### launchd で常駐させる

Gateway を LaunchAgent にします。

```bash
mkdir -p ~/Library/LaunchAgents ~/.local/state/herdr-mobile
# Tailscale（推奨）を使う場合:
LISTEN=$(tailscale ip -4):8765
# Tailscaleを使わない場合は、上の行を次のように置き換える:
# LISTEN=$(ipconfig getifaddr en0):8765
# （例: 192.168.1.20:8765）
sed -e "s#__HOME__#$HOME#g" -e "s#__LISTEN__#$LISTEN#g" \
  gateway/deploy/com.herdr-mobile.gateway.plist > ~/Library/LaunchAgents/com.herdr-mobile.gateway.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.herdr-mobile.gateway.plist

# 状態 / 再起動 / 停止
launchctl list | grep herdr-mobile
launchctl kickstart -k gui/$(id -u)/com.herdr-mobile.gateway
launchctl bootout gui/$(id -u)/com.herdr-mobile.gateway
```

ログ: `~/.local/state/herdr-mobile/gateway.log`（JSON、10MB × 3 世代でローテーション）

## 3. Firebase（通知）のセットアップ（任意）

通知まで有効にする場合だけ必要です。Firebase の設定は少し手間ですが、Gateway が Mac 上で FCM HTTP v1 を使って通知を送り、Android は通知受信後に会話をバックグラウンド取得します。これが「通知をタップしたら、すでに最新ログが読める」体験を支えています。

`google-services.json` とサービスアカウント秘密鍵 JSON は **リポジトリにコミットしないでください**（`.gitignore` 済み）。2つの JSON は同じ Firebase プロジェクトのものを使います。サービスアカウント秘密鍵は Gateway が動く Mac にだけ保存し、Android や QR には渡しません。

### 推奨: Mac のメニューバーアプリで取り込む

1. [Firebase コンソール](https://console.firebase.google.com/) でプロジェクトを作成する。
2. Android アプリを追加する。パッケージ名は `com.tohutohu.herdrcompanion`。
3. Android 用の `google-services.json` をダウンロードする。
4. プロジェクトの設定 → サービスアカウント → 「新しい秘密鍵を生成」でサービスアカウント JSON をダウンロードする。
5. `Herdr Companion Gateway.app` の「通知 · Firebaseを持ち込む」で、2つの JSON を選んで「取り込む」を押す。
6. Gateway を再起動し、ペアリング QR を発行し直して Android とペアリングする。

メニューバーアプリは Android に必要な公開設定（API key、App ID、Project ID、Sender ID）だけをペアリング時に渡します。共通配布 APK の再ビルドは不要です。Android 13 以降では、初回起動時に通知の許可も有効にしてください。

### CLI で取り込む場合

Gateway を停止した状態で、同梱またはビルド済みの Gateway に2つのファイルを渡します。

```bash
herdr-mobile-gateway import-firebase \
  --service-account "$HOME/Downloads/<project>-firebase-adminsdk-....json" \
  --android-config "$PWD/android/app/google-services.json"
```

このコマンドは、サービスアカウントが Android アプリと同じ Firebase プロジェクトに属することを検証し、Gateway の設定へ保存します。通常の CLI / launchd 構成では、サービスアカウントを次の場所に置くとそのまま使えます。

```bash
mkdir -p "$HOME/.config/herdr-mobile"
cp "$HOME/Downloads/<project>-firebase-adminsdk-....json" \
  "$HOME/.config/herdr-mobile/firebase-service-account.json"
chmod 600 "$HOME/.config/herdr-mobile/firebase-service-account.json"
```

Gateway を再起動して Android の設定を保存した後、登録確認とテスト通知を実行できます。

```bash
herdr-mobile-gateway devices
herdr-mobile-gateway notify-test
```

Firebase CLI と gcloud でファイルを作る場合の例:

```bash
PROJECT=herdr-client-android
firebase apps:create android "Herdr Companion" \
  --package-name com.tohutohu.herdrcompanion --project "$PROJECT"
firebase apps:sdkconfig ANDROID <表示された App ID> \
  --project "$PROJECT" --out android/app/google-services.json
gcloud iam service-accounts keys create \
  "$HOME/.config/herdr-mobile/firebase-service-account.json" \
  --iam-account firebase-adminsdk-fbsvc@"$PROJECT".iam.gserviceaccount.com \
  --project "$PROJECT"
```

個人用 APK に Firebase 設定をビルド時に内蔵する従来方式もあります。その場合だけ `android/app/google-services.json` を置き、`-PbundleFirebase=true` を付けてビルドします。共有 APK では、秘密情報を含めず QR ペアリング時にクライアント設定を受け取る方式を推奨します。

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
Tailscaleを使う場合はMagicDNS名またはTailscale IP（`http://100.x.y.z:8765`）、LANで使う場合はMacのIP（例: `http://192.168.1.20:8765`）、Tunnel経由なら`https://...`のTunnel URLを指定してください。

デバッグビルドは adb から設定を渡せます（日本語 IME で `adb shell input text` が変換されるのを避けるため）。受け口は adb にしか送れない receiver です:

```bash
adb shell am broadcast -n com.tohutohu.herdrcompanion/.DebugConfigReceiver \
  --es gateway_url "http://100.x.y.z:8765" --es token "$(herdr-mobile-gateway token)"
adb shell am start -n com.tohutohu.herdrcompanion/.MainActivity
```
Android 13 以降は通知の許可を求められます。

## 開発

```bash
# Claude サイドカー（Bun）。Gateway は実行ファイルと同じ場所、PATH、HERDR_CLAUDE_SIDECAR の順に探す
cd gateway/claude-sidecar
bun install && bun run typecheck && bun run build   # → dist/herdr-claude-sidecar

# Gateway
cd gateway
HERDR_CLAUDE_SIDECAR=$PWD/claude-sidecar/dist/herdr-claude-sidecar \
  go run ./cmd/herdr-mobile-gateway --debug --log-file ""   # 開発起動（:8765）
go test ./...
go test ./internal/providers/claude ./internal/providers/codex -update  # fixture の golden を更新
go vet ./...

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

- セッションにはターミナルがありません。TUI だけのダイアログ（Codex の「Implement this plan?」や非同期の質問キュー）もないので、次の指示を送ってください。アプリで回答できない要求は読み取り専用で表示します。ターンを中断して次の指示を送ってください。
- Gateway が動かしているセッションを別のターミナルで再開（`claude --resume`、`codex resume`）したり、ターミナルや Herdr のペインでまだ動いているセッションをアプリから再開したりしないでください（同じ履歴に 2 つのプロセスが書き込みます）。
- 通知の重複防止はメモリ上のみで、Gateway 再起動直後の状態は通知しません。
- バックグラウンド取得には Gateway が起動していて Android から到達できる必要があります。Android でアプリを「強制停止」している間は FCM 通知が届きません。
