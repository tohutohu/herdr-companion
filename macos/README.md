# Herdr Mobile for Mac

macOS 13以降のメニューバーアプリです。Go Gatewayを同梱し、起動・停止、Firebase設定の取り込み、任意のJevキー設定、AndroidとのQRペアリング、ログイン時起動を管理します。

## 使い方

1. MacとAndroidにTailscaleを用意し、同じtailnetに接続します。
2. MacのHerdrとClaude Code／Codexをセットアップします。Herdrのintegration設定は[ルートREADME](../README.md#1-herdr-のセットアップ)を参照してください。
3. DMGを開き、`Herdr Mobile.app`をApplicationsへドラッグして起動します。
4. メニューバーのターミナルアイコンを開き、Tailscale IPv4とポートを確認して「Gatewayを起動」。
5. 「ペアリングQRを表示」→ Androidの設定で「Scan Mac QR」→ 接続先を確認して「Pair」。

**Firebaseの秘密鍵もJevのキーも不要で起動・ペアリングできます。** 閲覧・送信・承認・セッション起動は通常どおり使えます。Firebase未設定ではプッシュ通知、Jev未設定では開始前のフォルダ確認だけが無効になります。

既存接続を変更する場合、Androidは保存後に一度閉じて開き直します。古い会話・画像キャッシュと通知を削除し、古い接続のバックグラウンド送信を破棄してから新しい設定で開始します。

QR読み取りはGoogle Play servicesを使用します。初回はスキャナーモジュールのダウンロードが必要な場合があります。手動URL・トークン接続も引き続き利用できます。

## 通知を有効にする場合（任意）

1. 自分のFirebaseプロジェクトでAndroidアプリを登録します。パッケージ名は **`com.tohutohu.herdrmobile`**。
2. Android用`google-services.json`と、同じプロジェクトのサービスアカウント秘密鍵JSONをダウンロードします。FCM HTTP v1 APIと送信権限を有効にします。
3. メニューバーの「通知」で2ファイルを選び「取り込む」。
4. QRを発行し、Androidをペアリングします。配布APKの再ビルドは不要です。

秘密鍵はMacの専用設定ディレクトリへ権限600でコピーします。APIキー・App ID・Project ID・Sender IDというAndroidクライアント用設定のみをペアリング先へ渡します。秘密鍵・Jevキーは渡しません。取り込み時はアプリが所有するGatewayだけを再起動します。

Firebase APIキーをAndroidアプリに制限する場合、配布APKのパッケージ名と署名証明書を許可してください。Play版ではPlay App Signingの証明書を使います。アプリ内のプッシュ登録結果を確認してください。

## Jev（任意）

メニューバーの「フォルダ確認」にTypeSafe APIキーを入力して保存します。「キーを削除」で無効化できます。指示・フォルダ情報・READMEなど・最近の会話の抜粋をTypeSafeへ送る機能です。詳しい対象は[ルートREADME](../README.md#開始前のフォルダ確認jev任意)を参照してください。キーなし・判定失敗でもセッションを開始できます。

## 既存CLI版との関係

- 設定: `~/.config/herdr-mobile/desktop/config.json`
- ログ・状態: `~/.local/state/herdr-mobile/desktop/`
- 既定ポート: **8766**（CLI版の8765と共存可能）
- Herdr本体は同梱・自動起動・停止しません。稼働中のHerdrサーバーを利用します。
- アプリ自身が起動したGatewayだけを管理します。既存LaunchAgentは変更しません。
- アプリを終了すると同梱Gatewayも終了します。ログイン時起動は設定で有効にできます。
- Gatewayがクラッシュした場合は状態が停止に変わります。「Gatewayを起動」で再起動してください。

## ビルド・DMG

Xcode Command Line Tools（Swift）、Go、macOSが必要です。

```bash
bash macos/scripts/build-dmg.sh
```

現在のMacのアーキテクチャ向けに`macos/build/Herdr-Mobile-arm64.dmg`または`Herdr-Mobile-x86_64.dmg`を作成します。Intel版はIntel環境でビルドしてください。

既定はローカル検証用のad-hoc署名です。一般配布にはDeveloper ID署名・Apple公証を行ってください。

```bash
SIGNING_IDENTITY='Developer ID Application: …' \
NOTARY_PROFILE='your-keychain-profile' \
bash macos/scripts/build-dmg.sh
```

署名用秘密鍵や公証パスワードはソースに入れず、Keychainに保存します。ビルドスクリプトは署名検証、任意の公証・staple、DMGのSHA-256出力まで行います。

## ペアリング仕様

- 認証済み`POST /v1/pairing`で256bitのランダムコードを発行。5分有効、1回のみ交換可能。
- QRは`herdr-mobile://pair?url=<Tailscale HTTP origin>#<code>`。アプリ内のスキャナーで読みます。
- コードは`POST /pair`の本文で送り、長期Bearer tokenと任意のFirebaseクライアント設定を受け取ります。
- 新QRの発行、無効化、Gateway再起動、Bearer token変更で以前のコードを無効化します。
- 公開ホスト、パス付きURL、短いコードを拒否し、交換時のリダイレクトを追いません。
- ペアリング先はTailscale IPv4限定です。HTTP区間はTailscaleによる暗号化を前提とします。
- 現段階では既存の共有Bearer tokenを渡します。端末単位の権限・失効には未対応です。

## 検証範囲

Goのペアリング有効期限・一度限り・並行交換・認証・鍵なし動作、Firebase設定の整合性、AndroidのQR解析を自動テストします。実Firebaseプロジェクトでの通知受信、実機カメラからのQR読み取り、署名済み配布物の別Macへの初回導入は別途確認が必要です。

## このMacの運用・更新

このMacはLaunchAgent版Gatewayからメニューバー版へ移行済みです。
`/Applications/Herdr Mobile.app`をログイン時に開き、同梱Gatewayを自動起動します。
接続先は移行前と同じ`100.99.15.34:8765`で、認証トークン・登録端末・Firebase秘密鍵・Jevキーをdesktop設定へ引き継いでいます。
HerdrサーバーのLaunchAgentは引き続き必要です。

Gatewayを更新するときは、メニューバーアプリを終了し、ビルドと置き換え後に開き直します。

```bash
osascript -e 'tell application id "com.tohutohu.herdrmobile.mac" to quit'
bash macos/scripts/build-dmg.sh
ditto "macos/build/Herdr Mobile.app" "/Applications/Herdr Mobile.app"
open "/Applications/Herdr Mobile.app"
```

終了後は子Gatewayの停止を確認してから置き換えます。旧GatewayのLaunchAgentは再登録しません。
CLIで診断するときも同じ実装・設定を使います。

```bash
HERDR_MOBILE_CONFIG="$HOME/.config/herdr-mobile/desktop/config.json" \
HERDR_MOBILE_STATE_DIR="$HOME/.local/state/herdr-mobile/desktop" \
  "/Applications/Herdr Mobile.app/Contents/Resources/herdr-mobile-gateway" devices
```
