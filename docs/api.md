# Herdr Companion Gateway API

Mac 上の Gateway（`gateway/`）が提供する HTTP API の仕様と、Mac の外（Android・
他端末・スクリプト）から接続するための情報をまとめる。実装の正は
`gateway/internal/api/server.go` と `gateway/internal/model/`。設計の背景は
[architecture.md](architecture.md) を参照。AI エージェントから使う CLI は [cli.md](cli.md)。

2.0 から Gateway はセッションをすべて自分で動かし（Claude Code・Codex のみ）、Herdr のペインを扱わない。
1.x にあったターミナル（`/v1/sessions/{id}/terminal`、`/v1/launches/{pane}/terminal`）、
`POST /v1/launches/{pane}/continue`、`POST /v1/sessions/{id}/migrate`、Session の `paneId` / `backend`、
起動・再開の `backend` はなくなった。

## 接続情報

### この Mac の Gateway

| 項目 | 値 |
|---|---|
| ベース URL | `http://100.99.15.34:8765`（Tailscale IP。MagicDNS 名は Pixel 上で解決できない） |
| 待ち受け | `100.99.15.34:8765` のみ（`config.json` の `listen`）。ループバック・LAN からは届かない |
| 実体 | `/Applications/Herdr Companion Gateway.app` に同梱された Gateway（メニューバーアプリが子プロセスとして起動） |
| 設定 | `~/.config/herdr-mobile/desktop/config.json`（mode 600） |
| ログ | `~/.local/state/herdr-mobile/desktop/gateway.log` |
| 疎通確認 | `curl http://100.99.15.34:8765/healthz` → `ok` |

新規インストールのメニューバーアプリは既定でポート `8766` を使う（この Mac は移行時の
`8765` を維持）。CLI の `serve` は `--listen` 未指定かつ設定が空なら `:8765`。

### Cloudflare Tunnel（tailnet の外から）

Tailscale に入っていない端末からは、名前付きトンネル `Herdr-Mobile-Gateway` 経由で同じ Gateway に届く。

| 項目 | 値 |
|---|---|
| ベース URL | `https://<公開ホスト名>`（`~/.cloudflared/herdr-gateway.yml` の `ingress[0].hostname`） |
| 転送先 | `http://100.99.15.34:8765`（上の Gateway そのもの。API・トークンは共通） |
| 実体 | LaunchAgent `com.herdr-mobile.cloudflare-tunnel`（`/opt/homebrew/bin/cloudflared --config ~/.cloudflared/herdr-gateway.yml tunnel run Herdr-Mobile-Gateway`、`KeepAlive`） |
| 認証情報 | `~/.cloudflared/<tunnel id>.json`（コミットしない） |
| ログ | `~/.local/state/herdr-mobile/desktop/cloudflared-tunnel.log` |
| 状態確認 | `launchctl list \| grep cloudflare-tunnel`、`curl https://<公開ホスト名>/healthz` → `ok` |

- Cloudflare Access は前段に置いていない。インターネットから届くので、守りは Bearer トークンだけになる（`/healthz` と `/pair` は認証なしで応答する）。トークンが漏れたら `token --rotate` する。
- 公開リポジトリなのでホスト名はここに書かない。上の設定ファイルで確認する。
- メニューバーアプリが作る QR の `url` は常に待ち受けアドレス（`http://100.99.15.34:8765`）で、トンネルの URL にはならない。トンネル経由で使う端末は、Android の設定で Gateway URL をトンネルの URL にし、同じトークンを使う。

### 認証

`/v1/*` はすべて Bearer トークン必須。ネットワーク経路に関係なく要求される。

```http
Authorization: Bearer <token>
```

- トークンは 32 バイト乱数の 16 進表記（64 文字）。一致しなければ `401 {"error":"unauthorized"}`。
- 取得・再発行（再発行後は各クライアントの設定も更新する）:

  ```bash
  export HERDR_MOBILE_CONFIG="$HOME/.config/herdr-mobile/desktop/config.json"
  GW="/Applications/Herdr Companion Gateway.app/Contents/Resources/herdr-mobile-gateway"
  "$GW" token            # 表示
  "$GW" token --rotate   # 再発行
  ```

- トークンをリポジトリ・ログ・URL に入れないこと。

### ネットワーク経路

| 経路 | URL の例 | 注意 |
|---|---|---|
| Tailscale（推奨） | `http://100.x.y.z:8765` / `http://<host>.<tailnet>.ts.net:8765` | WireGuard で暗号化される。両端末を同じ tailnet に入れる |
| 信頼できる LAN | `http://192.168.1.20:8765` | 平文 HTTP。Gateway の `listen` を LAN の IP にする必要がある |
| Cloudflare Tunnel 等 | `https://gateway.example.com` | 公開経路では必ず HTTPS。平文 HTTP をインターネットに直接出さない。この Mac の設定は上の節 |

Android エミュレータからホストのループバックは `10.0.2.2` で届く（使い捨て Gateway を
`127.0.0.1:18765` で動かした場合は `http://10.0.2.2:18765`）。本番 Gateway は Tailscale IP
でしか待ち受けないので、エミュレータからも `http://100.99.15.34:8765` を使う。

### QR ペアリング

トークンを手入力せずに端末を登録する流れ。招待は Gateway ごとに常に 1 件だけ有効。

1. Mac 側（メニューバーアプリの「ペアリングQRを表示」）が `POST /v1/pairing` を呼び、
   招待コード（64 桁 16 進、有効期限 5 分）を受け取る。
2. 次の形式のリンクを QR にして表示する。コードはフラグメントに入れ、サーバーへ送られる URL やログに残さない。

   ```text
   herdr-companion://pair?url=<URL エンコードした Gateway のベース URL>#<code>
   ```

   ベース URL は `http` か `https`、パスは `/` のみ、認証情報・クエリ・フラグメントなし。
3. Android が `POST <ベース URL>/pair` に `{"code":"<code>"}` を送る（リダイレクトは追わない）。
4. 成功すると招待は消費され、トークンなどが返る。

招待は「期限切れ・取り消し（`DELETE /v1/pairing`）・新しい招待の発行・使用済み・
トークン再発行・Gateway 再起動」のいずれかで無効になる。

### curl での呼び出し例

```bash
GW_URL=http://100.99.15.34:8765
TOKEN=$("$GW" token)
curl -s -H "Authorization: Bearer $TOKEN" "$GW_URL/v1/sessions" | jq
curl -s -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"text":"テストを実行して"}' "$GW_URL/v1/sessions/claude:<session-id>/messages"
```

## 共通仕様

- リクエスト・レスポンスとも JSON（`Content-Type: application/json`）。例外はアップロード・画像・ファイル本体。
- 時刻は RFC 3339（Go の `time.Time`）。
- 省略可能なフィールドは空のとき出力されない（`omitempty`）。クライアントは未知のフィールドを無視すること。
- JSON ボディの上限は概ね 1 MiB（ペアリング・デバイス・trust などは 64 KiB 以下）。
- エラーは `{"error": "<メッセージ>"}`。主なステータス:

| ステータス | 意味 |
|---|---|
| 400 | 不正な JSON・必須項目不足・不正なモデル名やフォルダ名・git 管理外での worktree 指定 |
| 401 | トークン不一致（`/pair` では招待コードが無効・期限切れ） |
| 403 | 許可されたルート外のファイルへのアクセス |
| 404 | セッション・ファイル・アップロード・保留中の起動が見つからない |
| 409 | セッションが動いていない（`offline`、プロセスが無いセッションの中断・モード巡回を含む）／既に動いている、質問が既に閉じた、作業ディレクトリが無い、同名フォルダが既にある、送信結果不明・送信 ID の内容不一致 |
| 413 | ファイル・アップロードが大きすぎる |
| 422 | そのプロバイダーが対応していない操作 |
| 503 | エージェント更新機能が無効、送信 receipt の永続保存が利用不能、Claude サイドカーや Codex app-server を起動できない |

### セッション ID

`<provider>:<native-id>` 形式。`provider` は `claude` / `codex` / `dsh`。
`native-id` は各エージェント自身のセッション（スレッド）ID。ターミナルや 1.x の Herdr のペインで始めた
セッションも同じ ID のまま一覧に出て、再開できる。

### 実行の仕組み

Gateway が動かすセッションは、Claude Code なら Agent SDK のサイドカー、Codex なら Gateway 自身の
`codex app-server`（stdio）で動く。どちらも Gateway の子プロセスで、Herdr は不要。

### セッションの状態（`status`）

| 値 | 意味 |
|---|---|
| `running` | 作業中 |
| `waiting_input` | 質問への回答待ち |
| `waiting_approval` | 承認待ち（プランの承認を含む） |
| `completed` | 最後のターンが完了 |
| `idle` | 待機中 |
| `failed` | 最後のターンが失敗 |
| `offline` | Gateway が動かしていない（ターミナルや 1.x の Herdr のペインで始めたものなど。直近 `offlineSessionDays` 日、既定 3 日以内に更新されたもの）。`resume` で同じ ID のまま Gateway が再開する |

Gateway が動かすセッションは、プロセスが無い間（Gateway の再起動後など）も `offline` にならず `idle` で、
`canSend: true` のまま一覧に残る。次のメッセージで同じ ID のまま新しいプロセスで再開する。
ターンの途中でプロセス（または Gateway）が止まったセッションは `failed` になり、`error` に理由が入る。
途中だったターンや操作は自動で再実行しない。

## エンドポイント一覧

| メソッド | パス | 認証 | 概要 |
|---|---|---|---|
| GET | `/healthz` | 不要 | 死活確認。`ok` を返す |
| POST | `/pair` | 不要 | 招待コードを交換してトークンを得る |
| POST / DELETE | `/v1/pairing` | 要 | 招待の発行／取り消し |
| GET | `/v1/sessions` | 要 | セッション一覧（`?archived=true` でアーカイブ済み） |
| POST | `/v1/sessions` | 要 | 新しいセッションを起動 |
| GET | `/v1/sessions/{id}` | 要 | セッション 1 件 |
| GET | `/v1/sessions/{id}/messages` | 要 | セッションとメッセージ |
| POST | `/v1/sessions/{id}/messages` | 要 | メッセージ送信 |
| GET | `/v1/message-delivery` | 要 | 永続 receipt による重複送信防止の対応状況 |
| POST | `/v1/sessions/{id}/respond` | 要 | 質問・承認への回答 |
| POST | `/v1/sessions/{id}/mode` | 要 | モードを次へ切り替え、または指定 |
| POST | `/v1/sessions/{id}/model` | 要 | 実行中セッションのモデルを変更 |
| POST | `/v1/sessions/{id}/effort` | 要 | 実行中セッションの推論強度を変更 |
| POST | `/v1/sessions/{id}/interrupt` | 要 | 実行中のターンを中断 |
| POST | `/v1/sessions/{id}/side-question` | 要 | 脇道の質問（btw、Claude のみ） |
| POST | `/v1/sessions/{id}/seen` | 要 | 既読にする |
| POST | `/v1/sessions/{id}/reload` | 要 | キャッシュを捨てて読み直し、質問の取りこぼしを診断 |
| POST / DELETE | `/v1/sessions/{id}/archive` | 要 | アーカイブ／解除 |
| POST | `/v1/sessions/archive` | 要 | 一括アーカイブ |
| POST | `/v1/sessions/{id}/resume` | 要 | 停止中のセッションを再開 |
| GET | `/v1/sessions/{id}/messages/{mid}/images/{n}` | 要 | メッセージ内の画像 |
| GET | `/v1/sessions/{id}/files` | 要 | ディレクトリ一覧 |
| GET | `/v1/sessions/{id}/files/content` | 要 | ファイル本体 |
| GET | `/v1/sessions/{id}/files/stat` | 要 | ファイル情報 |
| (プロキシ) | `http://localhost:<port>/…`、`CONNECT localhost:<port>` | 要（`Proxy-Authorization`） | Mac の localhost への転送（[下記](#localhost-の転送)） |
| POST | `/v1/launches/{launchId}/trust` | 要 | 起動時のフォルダ信頼の確認に回答 |
| GET | `/v1/models` | 要 | 起動時に選べるモデル・effort・モード |
| GET / POST | `/v1/directories` | 要 | 作業フォルダの一覧／作成 |
| POST | `/v1/directories/check` | 要 | 作業フォルダの事前チェック（Jev） |
| GET | `/v1/agents` | 要 | インストール済み CLI のバージョンと更新状況 |
| POST | `/v1/agents/{provider}/update` | 要 | CLI の更新を開始 |
| POST | `/v1/uploads` | 要 | 添付ファイルのアップロード |
| GET | `/v1/usage` | 要 | サブスクリプション利用枠（キャッシュ） |
| POST | `/v1/usage/refresh` | 要 | 利用枠を今すぐ読み直す |
| POST / DELETE | `/v1/devices` | 要 | FCM トークンの登録／解除 |
| GET | `/v1/capabilities` | 要 | 対応機能（常駐タスクの有無と版） |
| * | `/v1/resident/…` | 要 | 常駐タスク（[下記](#常駐タスク)） |

## ペアリング

### `POST /v1/pairing`

招待を発行する（既存の招待は置き換わる）。`201`、`Cache-Control: no-store`。

```json
{ "code": "<64桁16進>", "expiresAt": "2026-10-01T12:05:00+09:00" }
```

### `DELETE /v1/pairing`

招待を取り消す。`204`。

### `POST /pair`（認証不要）

```json
{ "code": "<64桁16進>" }
```

`200`:

```json
{
  "token": "<Bearer トークン>",
  "gatewayId": "<sha256(token) の16進>",
  "firebase": { "apiKey": "…", "applicationId": "1:…:android:…", "projectId": "…", "senderId": "…" }
}
```

`firebase` は Gateway に Android 用 Firebase 設定があるときだけ含まれる（サービスアカウントの秘密鍵は含まない）。
`gatewayId` は通知の送信元を、トークンを明かさずに識別するための値。

## セッション

### Session オブジェクト

```json
{
  "id": "claude:0f3c…",
  "provider": "claude",
  "providerName": "Claude Code",
  "project": "herdr-android-client",
  "title": "API ドキュメントを書く",
  "cwd": "/Users/…/herdr-android-client",
  "status": "running",
  "updatedAt": "2026-10-01T12:00:00Z",
  "lastMessage": "…",
  "model": "claude-opus-5-5",
  "effort": "high",
  "pendingModel": "sonnet",
  "settingsApply": "next_request",
  "mode": "Plan",
  "canSend": true,
  "archived": false,
  "unread": false,
  "context": { "usedTokens": 52000, "windowTokens": 200000, "usedPercent": 26 },
  "cost": { "usd": 1.23, "estimated": true }
}
```

| フィールド | 説明 |
|---|---|
| `canSend` | 今メッセージを送れるか（Gateway が動かしている。プロセスが無ければ送信時に再開する） |
| `unread` | 最後のターンが終わり、まだ見ていない（`seen` 未送信） |
| `translation` | セッションの有効な翻訳モード（boolean、無効時も `false` を返す）。開始時の明示指定を優先し、未指定なら Gateway の入力・出力翻訳のいずれかが有効かを返す。翻訳モデルの起動状態や原文表示の切替とは独立。旧 Gateway が返さない場合は不明として扱う |
| `background` | セッションが裏で動かし続けている作業。`[{id, kind, description, startedAt}]`、無ければ省略。`kind` は `agent` / `shell` / `other`。`status` とは独立で、`completed` や `idle` でも空とは限らない。`startedAt` は Gateway が最初に見た時刻。Claude Code のセッションのみ |
| `model` / `effort` | 最後の応答が使ったモデルと推論強度 |
| `pendingModel` / `pendingEffort` | 変更を要求したがまだ使われていない値。適用されると消える |
| `settingsApply` | 保留中の変更が効く時点。`next_request`（Claude: 次のモデル呼び出し。実行中のターン内でも）／`next_turn`（Codex: 次のターン） |
| `error` | `failed` のセッションの理由（プロセスの終了など） |
| `context` | コンテキストウィンドウの使用量。未報告なら省略 |
| `cost` | API 定価換算の金額。`estimated: true` はトークン数から Gateway が算出した値 |

### `GET /v1/sessions`

動作中と、最近更新されたオフラインのセッション。アーカイブ済みは含まない。

```json
{ "sessions": [Session, …] }
```

`?archived=true` でアーカイブ済みを返す。`offset` と `limit`（0 以上の整数）を付けると
1 ページ分を返し、続きがあれば `nextOffset` を付ける。`limit` なしなら全件。

```json
{ "sessions": [Session, …], "nextOffset": 50 }
```

### `POST /v1/sessions`

エージェントを Gateway が起動する。`cwd` は `workspaceRoots`（未設定ならホーム）配下に限る。

```json
{
  "provider": "claude",
  "cwd": "/Users/…/project",
  "prompt": "最初の指示",
  "uploads": ["<upload id>"],
  "model": "claude-sonnet-5-5",
  "effort": "high",
  "mode": "plan",
  "trust": true,
  "worktree": false,
  "translation": false
}
```

| フィールド | 必須 | 説明 |
|---|---|---|
| `provider`, `cwd` | ○ | `provider` は `claude` / `codex` / `dsh` |
| `prompt` | | 最初のプロンプト。`uploads` は最初のプロンプトに添付される |
| `model` / `effort` / `mode` | | `GET /v1/models` の `id`。空ならエージェントの既定 |
| `trust` | | フォルダ信頼を代わりに承認する（信頼の記録は書かない）。`false` のとき、エージェントが一度も信頼していないフォルダ（Claude: `~/.claude.json`、Codex: `config.toml` の記録を読むだけ）なら起動せずに `trustRequired` と `launchId` を返す |
| `translation` | | `true` はこのセッションのプロンプト英訳・日本語表示を有効にし、`false` は両方を無効にする。省略／`null` は Gateway のモード設定に従う。初回文から適用し、信頼確認・再起動・再開後も保持する。モデルは Gateway メニューから事前に起動する |
| `worktree` | | `cwd` のリポジトリに新しい git worktree（`worktree/<名前>` ブランチ）を作り、そこで起動する。Gateway が git で作る（状態ディレクトリの `worktrees/` 配下） |

`201`（StartResult）:

```json
{ "sessionId": "claude:…", "warning": "…" }
{ "launchId": "launch-claude-1a2b3c", "trustRequired": true }
```

`sessionId` は起動前に決めるので、起動すれば必ず返る。最初のプロンプトだけ失敗した場合は
`sessionId` と `warning` を返す。

### `POST /v1/launches/{launchId}/trust`

`trustRequired` で止まった起動に回答する。`{"trust": true}` で続行、`false` で取りやめ（`launchId` だけを返す）。
`200` で StartResult。保留中の起動がなければ `404`。

### `GET /v1/sessions/{id}`

Session を 1 件返す。

### `POST /v1/sessions/{id}/archive` / `DELETE /v1/sessions/{id}/archive`

POST: 動作中ならプロセスを終了して停止し、失うものがなければ起動時の worktree を削除してアーカイブする。
worktree は、他のセッションが使っている、または未コミット・未追跡の変更がある場合は残し、`warning` で理由を返す。ブランチは常に残す。
レスポンスは Session に `warning`（worktree を残した理由など）を加えたもの。
DELETE: アーカイブを解除し Session を返す。

### `POST /v1/sessions/archive`

```json
{ "ids": ["claude:…", "codex:…"] }
```

重複は無視、空文字は `400`。`{"sessions": [Session + warning?, …]}`。

### `POST /v1/sessions/{id}/resume`

停止中（`offline`）・アーカイブ済みのセッションを、同じ ID のまま新しいプロセスで Gateway が再開し、アーカイブから外す。
ターミナルや 1.x の Herdr のペインで始めたセッションも再開できる（そこでまだ動いていれば先に終了させる。2 つのプロセスが
同じ履歴に書き込むため）。ボディは `{}` か省略（1.x の `trust` / `backend` は無視する）。
`201` で StartResult（`sessionId`）。プロセスが動いていれば `409`。

## メッセージ

### `GET /v1/sessions/{id}/messages?after=<message id>`

```json
{ "session": Session, "messages": [Message, …] }
```

- `after` を付けると、そのメッセージ以降（そのメッセージ自身を含む）だけを返す。進行中の更新を拾うため。未知の ID、または `queued:` で始まる仮 ID なら全件。キュー済みプロンプトは常に末尾に置かれるため、差分取得の起点には最後のキュー済みでないメッセージを使う。
- `ETag` を返す。`If-None-Match` が一致すれば `304 Not Modified`（ポーリング向け）。

### Message / Block

```json
{
  "id": "…",
  "role": "assistant",
  "timestamp": "2026-10-01T12:00:00Z",
  "queued": false,
  "blocks": [
    { "type": "text", "text": "…" },
    { "type": "image", "url": "/v1/sessions/claude:…/messages/<mid>/images/0" },
    { "type": "file", "path": "src/main.go", "line": 42, "size": 1024 },
    { "type": "session_message", "text": "確認しました。", "senderName": "reviewer-a", "sessionId": "claude:…" },
    { "type": "interaction", "interaction": Interaction }
  ]
}
```

- `role`: `user` / `assistant` / `tool` / `system`。
- `timestamp`: その発言の時刻。Codex の item は個別の時刻を持たないため、ターン開始時刻が入る。ただし完了したターンの最後のメッセージにはターンの完了時刻が入るので、クライアントはユーザー発言との差からターンの所要時間を求められる。
- `queued`: エージェントが受け取ったがまだ会話に取り込んでいないプロンプト（Claude Code の作業中キュー）。
- Block は `type` ごとに該当フィールドだけが入る平坦な union。未知の `type` は `text` として描画すること。
- 画像の `url` は Gateway 相対。ベース URL を前に付け、Bearer トークン付きで取得する。
- `session_message` は別の Claude セッションから受け取った本文。`role` は `system` とし、ユーザーの発言と区別する。`senderName` は任意。送信時刻以前から存在する送信元の登録とローカル履歴を確認できた場合だけ `sessionId` を返し、そのセッション画面を開ける。登録がなくなった場合や PID 再利用で特定できない場合はリンクなしで本文を表示する。

### Interaction（質問・承認）

```json
{
  "id": "…",
  "type": "questions",
  "kind": "plan",
  "state": "pending",
  "title": "…",
  "detail": "…",
  "supported": true,
  "answer": "…",
  "decisions": ["approve", "approve_session", "deny"],
  "questions": [
    {
      "id": "q1",
      "type": "select",
      "header": "…",
      "question": "どちらにしますか？",
      "options": [{ "label": "A", "description": "…", "preview": "…" }],
      "allowOther": true,
      "otherLabel": "…"
    }
  ]
}
```

| フィールド | 値 |
|---|---|
| `type` | `questions` / `approval` |
| `kind` | `plan` は完成したプランの承認（questions 形式だが承認扱い） |
| `state` | `pending` / `answered` / `closed`（未回答のまま操作不能になった） |
| `supported` | `false` なら表示のみ（アプリでは答えられない。ターンを中断して次の指示を送る） |
| `questions[].type` | `select` / `multiselect` / `text` |

### `POST /v1/sessions/{id}/messages`

```json
{ "text": "続けて", "uploads": ["<upload id>"], "requestId": "<optional client UUID>" }
```

テキストも添付も空なら `400`。`202 {"ok": true}`。翻訳して送信した場合は `sentText` にエージェントへ送った本文を返す。クライアントは日本語の送信待ちと英語の履歴を同じメッセージとして照合できる。旧Gatewayと成功済みreceiptの再応答ではこのフィールドは省略される。リクエストを受けた後はクライアントが切断しても送信を完了させる（最大 2 分）。
アーカイブ済みのセッションは送信成功後に自動でアーカイブ解除する。送信失敗時はアーカイブを維持する。プロセスが無いセッションは、同じ ID のまま新しいプロセスで再開してから送る。

`requestId` は任意で、英数字・`_`・`-` の1〜128文字。省略時は従来の送信動作を維持する。
対応する Gateway では、同じ認証トークンに対する同一 ID・同一セッション・同一本文・同一添付の
受付済み要求に `202` を再応答し、provider を再実行しない。ID を別内容に使うと `409`。
入力と送信先を検証した後、provider を呼ぶ前に receipt を永続化するため、送信途中の Gateway 再起動も
自動的な二重実行を起こさない。ただし provider のエラーや送信途中の中断では、送信済みかを断定できず
`409`（`Delivery unconfirmed...`）となる。会話を確認するまで新しい ID で送り直さない。
receipt を保存できない場合は送信を開始せず `503`。成功状態の保存に失敗した場合は `409` とする。

### `GET /v1/message-delivery`

```json
{ "idempotency": true }
```

送信前に対応状況を確認する。receipt store を持たない構成は `false`、旧 Gateway は `404` を返す。
旧 Gateway は未知の `requestId` を無視するため、この確認なしに POST を自動再試行してはいけない。
Android の通知返信は対応 Gateway に同じ ID を渡して再試行し、旧 Gateway では一度送信を始めた後の
通信断・プロセス再開を「結果不明」として止める。通常の画面内 Outbox は従来の送信動作を維持する。

receipt は `HERDR_MOBILE_STATE_DIR/message-delivery/`（Mac メニューバーアプリでは通常
`~/.local/state/herdr-mobile/desktop/message-delivery/`）に保存する。ファイル名はトークン由来の
識別値と送信 ID の hash、内容は要求の hash と受付済みフラグのみで、本文・添付内容・トークンは保存しない。
未確定 receipt を消すと遅延した再試行が実行され得るため、自動期限削除は行わない。
再起動時も同じ state 領域を使用することが重複防止の前提となる。

### `POST /v1/sessions/{id}/respond`

```json
{
  "interactionId": "…",
  "answers": { "q1": { "selected": ["A"], "text": "自由入力" } },
  "decision": "approve"
}
```

質問には `answers`（質問 ID → 回答）、承認には `decision`（`approve` / `approve_session` / `deny`）。`202 {"ok": true}`。

### `POST /v1/sessions/{id}/mode`

モード（Plan、Accept edits など）を SDK / app-server で切り替える。ボディが空なら次のモードへ進め、
`{"mode": "<GET /v1/models の modes の id>"}` なら直接指定する（Codex は次のターンから）。`202`。
プロセスが無いセッションでは、指定したモードを記録して再開時に使う（巡回は `409`）。カタログに無い値は `400`、非対応は `422`。

### `POST /v1/sessions/{id}/model` / `POST /v1/sessions/{id}/effort`

```json
{ "model": "sonnet" }
{ "effort": "high", "model": "sonnet" }
```

セッションのモデル／推論強度を、セッション ID と履歴を保ったまま変える。値は `GET /v1/models` の
カタログで検証し、無いもの（モデルが対応しない推論強度を含む）は `400 {"error": "invalid setting: … choose one of: …"}`
で拒否する。別の値に置き換えることはしない。`effort` の `model` は省略時、保留中のモデル、なければ現在のモデル。
`200` で更新後の Session（`pendingModel` / `pendingEffort` / `settingsApply`）。

- Claude Code: `Query.setModel()` と `applyFlagSettings({effortLevel})`。次のモデル呼び出しから（実行中のターン内でも）。セッション限りで、ユーザーの既定（`settings.json`）は変えない。
- Codex: 値を保持し、次の `turn/start` に `model` / `effort` を付ける。実行中のターンは変えない。

プロセスが無いセッションでは、値を記録して再開時に使う。`offline` のセッションは `409`。

### `POST /v1/sessions/{id}/interrupt`

実行中ターンを中断する（Claude: `Query.interrupt()`、Codex: `turn/interrupt`）。セッションは残る。`202`。
プロセスが無いセッションと `offline` のセッションは `409`。

### `POST /v1/sessions/{id}/side-question`

```json
{ "question": "なぜ receipts.go を読んだ？", "history": [{ "question": "いま何してる？", "answer": "…" }] }
```

Claude Code の `/btw` と同じ脇道の質問。セッションの会話を文脈に、ツールを使わずに答える。実行中のターンは止めず、
質問も答えもセッションの履歴に残らない（SDK の `Query.askSideQuestion`）。Gateway も何も保存しないので、続けて聞くときは
それまでの問答をクライアントが `history`（新しいものが後、最大 50 件）で送り返す。

- 答えが出るまで待って `200 {"answer": "…", "synthetic": false}` を返す（最長 3 分）。`synthetic` はモデルを使わずに
  エージェントが作った答え（答えられない理由など）。接続が切れると問い合わせを取り消す。
- プロセスが無いセッションは、メッセージと同じく再開してから聞く。
- 空の質問は `400`。会話がまだ無いセッションと `offline` のセッションは `409`。Codex のセッションは `422`。

### `POST /v1/sessions/{id}/seen`

既読にする。Gateway が動かすセッションの `unread` を消す（それ以外は何もしない）。`200 {"ok": true}`。

### `POST /v1/sessions/{id}/reload`

```json
{ "shown": ["<アプリが表示していた pending の interaction id>"] }
```

プロバイダーの表示用キャッシュを捨ててメッセージを全件読み直す。`200 {"session", "messages", "findings": [{"kind", "message"}]}`（`messages` は `after` なしの `GET …/messages` と同じ）。
読み直しの前後で質問・承認の取りこぼしを調べ、見つかったものを `findings` に返し、dead letter（`missed_interaction`）に記録する。

| `kind` | 意味 |
|---|---|
| `not_shown` | 読み直すと pending なのに `shown` に無かった（アプリのポーリングの取りこぼし）。`shown` を省くと調べない |
| `stale_cache` | キャッシュを使った読み取りに無い pending が、読み直すと現れた |
| `uncaptured` | `waiting_input` / `waiting_approval` なのに、アプリで答えられる（`supported`）pending が無い |

### `GET /v1/sessions/{id}/messages/{mid}/images/{n}`

画像のバイト列。`Content-Type` は `image/*`（それ以外は `application/octet-stream`）、`Cache-Control: private, max-age=86400`。

## ファイル

読めるのはセッションの `cwd`、アップロード用ディレクトリ、プロバイダーが参照するディレクトリ（Claude の保存済みプランなど）の配下だけ。外は `403`。

| エンドポイント | パラメータ | レスポンス |
|---|---|---|
| `GET /v1/sessions/{id}/files` | `path`（省略でルート） | `{"root": "<cwd>", "entries": [{"name","path","isDir","size"}]}`。`path` はルートからの相対 |
| `GET /v1/sessions/{id}/files/stat` | `path`（必須） | `{"path","name","size","contentType","previewable"}` |
| `GET /v1/sessions/{id}/files/content` | `path`（必須）、`download=1` | ファイル本体。通常は 10 MiB まで（超えると `413`）。`download=1` はサイズ無制限・`Content-Disposition: attachment`・Range 対応 |

`previewable` は 10 MiB 以下の `image/*` または `text/*`。

## localhost の転送

エージェントが Mac で起動した開発サーバー（`http://localhost:5173` など）をスマホで開くため、
Gateway は **Mac のループバック宛てに限った HTTP プロキシ**としても動く。ページの origin は
`http://localhost:<port>` のままなので、絶対パス・POST・Cookie・WebSocket が Mac のブラウザと同じように動く。

- 同じ待ち受けポートで、絶対形式の URL（`GET http://localhost:3000/ HTTP/1.1`）と `CONNECT localhost:3000`
  （HTTPS や WebSocket のトンネル）をプロキシ要求として受ける。通常の API パスとは衝突しない。
- 宛先は `localhost`・`127.0.0.0/8`・`::1` の明示ポートだけ。ほかは `403`、待ち受けがなければ `502`。
- 認証は `Proxy-Authorization: Basic base64(<任意>:<token>)` または `Bearer <token>`。ないと
  `407` と `Proxy-Authenticate: Basic realm="Herdr Companion"`。`Proxy-Authorization` は転送先に渡さない。
- プロキシなので、Gateway に直接届く経路（Tailscale）でだけ使える。Cloudflare Tunnel は絶対形式の
  URL と `CONNECT` を通さない。トンネル経由で API を使うクライアントのため、`GET /v1/capabilities` は
  直接届くアドレスを `localhostProxy`（例 `"http://100.99.15.34:8765"`）で返す。`listen` が 1 つの
  インターフェイスを指すときはそのアドレス、全インターフェイスなら直接届いた要求の Host、どちらでもなければ出さない。
  アプリは http:// の Gateway ならそれ自体を、https:// ならこのアドレスをプロキシにする。
- Android アプリは WebView の localhost 宛て通信だけを Gateway に向ける（androidx.webkit の
  `ProxyController`、reverse bypass）。入口はチャットの `http://localhost:…` のリンクだけで、
  Mac で待ち受けているポートの一覧は出さない（セッションと関係のないものまで見えるため）。

## 起動の準備

### `GET /v1/models?provider=claude`

```json
{
  "models": [{ "id": "…", "name": "…", "description": "…", "default": true, "efforts": [Effort, …] }],
  "efforts": [{ "id": "high", "name": "High", "description": "…", "default": true }],
  "modes": [{ "id": "plan", "name": "Plan", "description": "…", "default": false }]
}
```

モデル個別の `efforts` が空ならカタログの `efforts` が適用される。`modes` が空なら起動時に選べない。

### `GET /v1/directories?path=`

`path` 省略で `workspaceRoots` の一覧、指定するとそのサブフォルダ。

```json
{ "path": "/Users/…/workspace", "parent": "/Users/…", "entries": [{ "name": "app", "path": "/Users/…/workspace/app" }], "git": true }
```

`git: true` は `path` が git の作業ツリー内にある（`worktree` 起動ができる）ことを示す。

### `POST /v1/directories`

```json
{ "parent": "/Users/…/workspace", "name": "new-project" }
```

`201 {"path": "/Users/…/workspace/new-project"}`。同名があれば `409`。

### `POST /v1/directories/check`

```json
{ "cwd": "/Users/…/project", "prompt": "最初の指示" }
```

`200 {"verdict": "match", "historyCount": 3}`。`verdict` は `match` / `mismatch` / `unknown` / `unavailable` / `disabled`。
読み取り専用の助言で、起動を止めたりフォルダを変えたりはしない。Jev の API キーが無ければ `disabled`。

## エージェント CLI の更新

### `GET /v1/agents`

```json
{ "agents": [{ "provider": "claude", "version": "2.1.274", "state": "idle", "output": "…", "error": "…" }] }
```

`state` は `idle` / `running` / `succeeded` / `failed`。対象は `claude` / `codex`。

### `POST /v1/agents/{provider}/update`

更新コマンド（`claude update`、`codex update`）を開始する。`202` で上と同じ要素 1 件。
実行中に繰り返し呼ぶと同じジョブを共有する。未知のプロバイダーは `400`。

## アップロード

### `POST /v1/uploads`

ボディはファイルの生バイト列。`Content-Type` にファイルの型、任意で
`Content-Disposition: attachment; filename="a.png"` で名前を付ける（無ければ型から拡張子を決める）。
上限 500 MiB（超えると `413`）。`Content-Length` が不明な chunked body でもストリームしながら
上限を検査する。24 時間後に削除される。Android はファイルを一度に ByteArray にせず
URI のストリームから送る。サイズ不明の文書も chunked で送信できる。

```json
{ "id": "<upload id>", "name": "a.png" }
```

返った `id` をメッセージ送信やセッション起動の `uploads` に渡す。画像はエージェントにネイティブ添付し、それ以外はプロンプト中でパスを示す。

## 利用枠

### `GET /v1/usage`

バックグラウンドで定期取得（既定 5 分ごと）したキャッシュを返す。取得には CodexBar CLI を使う。

```json
{
  "providers": [
    {
      "provider": "claude",
      "displayName": "Claude",
      "plan": "Max",
      "account": "…",
      "windows": [{ "key": "primary", "label": "5h", "scope": "…", "usedPercent": 42, "windowMinutes": 300, "resetsAt": "…" }],
      "updatedAt": "…",
      "error": "…"
    }
  ],
  "fetchedAt": "…",
  "error": "…"
}
```

最新の取得に失敗しても直前の値を残し、`error` に理由を入れる（`fetchedAt` は最後に成功した時刻）。

### `POST /v1/usage/refresh`

今すぐ読み直す（約 10 秒）。同時の呼び出しは 1 回の読み取りを共有する。形式は上と同じ。

## メッセージの手動翻訳

### `POST /v1/translation/text`

Bearer認証付きで `{ "text": "English report" }` を送ると、ローカルPLaMoで日本語へ翻訳し、`{ "text": "日本語の報告" }` を返す。セッションの翻訳モードとは独立し、モード・履歴・入力原文の保存は変更しない。原文は空でない128 KiB以下とし、不正入力は400、モデル停止・翻訳失敗・機能未提供は503。最大140秒待つ。成功した訳は既存の翻訳キャッシュで再利用する。

## 通知（FCM）

Android の RemoteInput 返信は、本文をアプリ専用ファイルへ atomic に保存し、WorkManager Data には
UUID のみを渡す。長文を Data の10KB制限で失わず、同じセッションの返信を入力順で処理する。
先行返信の恒久エラーは通知で報告して scheduler 上は終端成功とし、既に待機している後続返信を止めない。
送信結果が不明な場合はその旨を表示し、即時の返信 action を外して会話の確認へ誘導する。
通知履歴は短いプレビューで、送信本文は切り詰めない。

アプリ起動時に保存・enqueue 間の中断を回復し、実行中・待機中 work の tag がある場合は重複登録しない。
本文が残っている場合は scheduler の成功だけで配信済みと判断せず、再登録して attempted/receipt を再確認する。
成功した本文ファイルと古い接続の本文ファイルは削除する。失敗・結果不明の終端本文は30日保持し、
オフラインで未処理の本文を経過時間だけで消さない。WorkManager で明示的にキャンセル済みの返信は再登録しない。
終端ファイルはローカル復旧用であり、現時点ではアプリ内の草稿復元画面はない。

### `POST /v1/devices`

```json
{ "name": "Pixel 7a", "fcmToken": "…" }
```

`name` 省略時は `android`。`200 {"ok": true}`。

### `DELETE /v1/devices?fcmToken=…`

登録を解除する。`200 {"ok": true}`。

### プッシュの内容

セッションが `completed` / `waiting_input` / `waiting_approval` / `failed` に変わったとき、
登録済みの端末へ data-only メッセージを送る。値はすべて文字列。

```json
{
  "sessionId": "claude:…",
  "status": "waiting_approval",
  "title": "Claude Code needs approval",
  "body": "<タイトルまたはプロジェクト>\n<最後の発言（全体で300文字まで）>",
  "provider": "claude",
  "providerName": "Claude Code",
  "project": "herdr-android-client",
  "canSend": "true",
  "gatewayId": "…",
  "report": "{\"id\":\"…\",\"role\":\"assistant\",\"timestamp\":\"…\",\"blocks\":[{\"type\":\"text\",\"text\":\"…\"}]}"
}
```

`report` は最後の報告（テキストを持つ最新の assistant メッセージ）を、テキストブロック1つにまとめた
メッセージ JSON。FCM の data 上限 4096 バイトに収まるよう、ペイロード全体が 3800 バイト以下になるまで
本文を切り詰め、末尾に `…` を付ける。会話を読めないときや報告がないときは省く。
アプリは会話を取得する前でもこれを画面に出し、取得した会話で置き換える。

表示翻訳が有効なセッションでは、通知のタイトル・本文・添付報告を送信前に翻訳してから容量に合わせる。
添付報告の `originalText` に原文も入る（長い場合は訳文と原文の両方を切り詰める）。
翻訳待ちは最大90秒で、停止・失敗時は原文で配信する。通知配信は状態監視とは別に動き、同一セッション内の順序を維持する。
常駐のプッシュ通知にはGateway全体の表示翻訳設定を適用し、保存済みOutboxの原文・ID・配信済み端末情報は変更しない。

常駐タスクの通知は別の形（`type: "resident"`）で、`sessionId` を含まない。[常駐タスク](#常駐タスク)を参照。

## 常駐タスク

継続して任せるタスク（常駐）の API。動作の詳細（予定、実行、復旧、権限、通知）は [resident.md](resident.md)。
すべて Bearer 認証。`/v1/sessions` とは独立していて、常駐の実行はセッション一覧に現れない（昇格した Run を除く）。
Run と会話のメッセージには `conversation`（`kept`: 昇格できる、`promoted`: `sessionId` のセッションになった、
会話を残す前の Run では省略）が付く。

### 対応の判別: `GET /v1/capabilities`

```json
{"messageIdempotency": true, "resident": {"version": 1, "providers": ["claude", "codex", "dsh"], "defaultTimezone": "Asia/Tokyo"}}
```

`resident` がない（または `/v1/capabilities` が 404 の）Gateway は常駐タスクに対応していない。アプリは 404 や通信エラーを
「タスク 0 件」として扱わない。

### エラー

`{"error": "理由", "code": "…"}`。`code` は `invalid`（400、理由付き。無効な cron・timezone・モデル・effort・範囲外の cwd など）、
`not_found`（404）、`conflict`（409。版の不一致、状態が合わない操作、requestId を別内容で再利用）、
`trust_required`（409。エージェントが信頼していないフォルダ。`trust: true` で再送）、`forbidden`、`too_large`、`internal`。

### 冪等性と競合

- タスク作成・手動実行・回答は `requestId`（8–100 文字の `[A-Za-z0-9._:-]`）を受け付ける。同じ ID の再送は最初の結果を返し、
  同じ ID で内容が違えば 409。通信の再試行で Run や回答が増えない。
- 更新（PATCH）と Memory の保存は `expectedVersion` が現在の版と一致したときだけ成功する（不一致は 409）。

### エンドポイント

| メソッド | パス | 概要 |
|---|---|---|
| GET | `/v1/resident/tasks` | タスク一覧 `{tasks[]}`（ETag 対応） |
| POST | `/v1/resident/tasks` | 作成 `{name, instruction, provider, model?, effort?, cwd, schedule, permissions?, notify?, limits?, precheck?, enabled?, trust?, requestId?}` → Task（201） |
| GET | `/v1/resident/tasks/{id}` | `{task, memory, wakeups[], upcoming[3], recentRuns[], pendingQuestion?}` |
| PATCH | `/v1/resident/tasks/{id}` | 変更（作成と同じ項目、省略は維持）`{expectedVersion, …}` → Task |
| DELETE | `/v1/resident/tasks/{id}` | 削除（以後起動しない、実行中の Run は停止） |
| POST | `/v1/resident/tasks/{id}/pause` / `resume` | 自動起動の一時停止／再開 → Task。pause に `{until}`（RFC 3339、未来かつ 366 日以内）を付けるとその時刻に自動で再開する。省略すると再開するまで止める |
| GET | `/v1/resident/tasks/{id}/messages?before=&limit=` | 会話 `{messages[], more}`（古い順） |
| POST | `/v1/resident/tasks/{id}/read` | 既読 `{messageId?}`（省略で最新まで） |
| GET | `/v1/resident/tasks/{id}/runs?before=&limit=` | 実行履歴 `{runs[], more}`（新しい順、skipped を含む） |
| POST | `/v1/resident/tasks/{id}/runs` | 今すぐ実行 `{requestId?}` → 202 `{run, merged}` |
| GET / PUT | `/v1/resident/tasks/{id}/memory` | Memory の取得／保存 `{expectedVersion, summary?, state?}` |
| GET | `/v1/resident/tasks/{id}/artifacts` | 成果物 `{artifacts[]}` |
| GET | `/v1/resident/tasks/{id}/notifications` | このタスクの通知 Outbox |
| GET | `/v1/resident/notifications?limit=` | 全タスクの新しい通知（Mac UI が自前で通知を出すのに使う） |
| GET | `/v1/resident/artifacts/{id}/content[?download=1]` | 成果物の内容（タスクのフォルダ内だけ。配信時にも再検証） |
| GET | `/v1/resident/runs/{id}` | Run 1 件（attempts, artifacts, question を含む） |
| POST | `/v1/resident/runs/{id}/cancel` | その Run だけを停止（待機中なら取り消し、実行中なら中断して終了を待つ） |
| POST | `/v1/resident/runs/{id}/dismiss` | 成否不明（`needs_reconciliation`）の Run を確認済みにする。再送はしない |
| POST | `/v1/resident/runs/{id}/promote` | Run の会話を通常のセッションにして再開する → `{sessionId}`。終わっていない Run は 409、会話を残していない Run は 400（[resident.md](resident.md#セッションへの昇格)） |
| POST | `/v1/resident/questions/{id}/answers` | 判断待ちへの回答 `{text, requestId?}` → 202 `{message, run}`。期限切れ・回答済みは 409 |
| POST | `/v1/resident/schedule/preview` | `{schedule, count?}` → `{next[], schedule}`（保存しない） |

### Task

```json
{
  "id": "task_…", "name": "CI監視", "instruction": "…", "provider": "claude", "model": "haiku", "effort": "low",
  "cwd": "/Users/me/workspace/app",
  "schedule": {"kind": "weekly", "time": "07:00", "days": ["mon", "thu"], "timezone": "Asia/Tokyo"},
  "permissions": {"profile": "workspace_write", "allowedTools": ["Bash(gh pr view:*)"]},
  "notify": {"mode": "changes", "onFailure": true, "onQuestion": true, "cooldownMinutes": 0, "quietStart": "23:00", "quietEnd": "07:00"},
  "limits": {"maxRunMinutes": 30, "maxRunsPerDay": 12, "expiresAt": null},
  "precheck": {"command": "python3 scrape.py", "timeoutSeconds": 60},
  "enabled": true, "pausedUntil": null, "version": 3, "trustedAt": "…",
  "state": "idle", "nextRunAt": "…", "nextWakeReason": "schedule", "lastSuccessAt": "…",
  "lastRun": {"id": "run_…", "status": "completed", "outcome": "unchanged", "summary": "…"},
  "latestReport": {"runId": "run_…", "outcome": "changed", "summary": "…", "at": "…"},
  "pendingQuestion": null, "unread": 1, "runsToday": 2, "attention": null
}
```

- `schedule.kind`: `manual` / `once`（`at`）/ `daily`（`time`）/ `weekly`（`days`, `time`）/ `cron`（`cron`）。`timezone` は IANA 名。
- `state`: `idle`（稼働中）/ `queued` / `running` / `waiting_user` / `needs_attention` / `paused`。
- `pausedUntil`: 期限付きで一時停止中のタスクが自動で再開する時刻（無期限の停止・稼働中は省略）。`pausedUntil` を返さない古い Gateway は `until` を無視して無期限で止める。
- 作成に必要なのは `instruction`・`provider`・`cwd`・`schedule` だけ。`name` を省くと指示の 1 行目、`permissions` は
  `workspace_write`（ネットワークは常に可、`network` は無視）、`notify` は判断待ち・失敗も通知する既定、`limits` は 30 分・12 回。
- `precheck`（省略可）: 自動起動の前に Gateway が実行するコマンド（[resident.md](resident.md#事前チェック)）。`command` は 4 KiB まで、
  `timeoutSeconds` は 1–600（既定 60、`maxRunMinutes` 未満）。PATCH で `{"command": ""}` を送ると外す。古い Gateway は未知の項目として 400 を返すので、アプリは設定・解除するときだけ送る。

### Run

`status`: `queued` / `running` / `completed` / `waiting_user` / `failed` / `cancelled` / `timed_out` / `needs_reconciliation` /
`skipped` / `dismissed`。`outcome`: `changed` / `unchanged` / `blocked` / `failed`。`errorKind`: `contract`（結果契約違反）/
`provider` / `permission` / `timeout` / `interrupted` / `config` / `agent` / `precheck`（事前チェックのコマンドが失敗）。
事前チェックが条件なし（exit 1）で終えた Run は `skipped` で、`startedAt` があり、`summary` に理由、`report` にコマンドの出力が入る。`triggers`: `schedule` / `catch_up` / `self_wake` /
`manual` / `answer`（以前の Run には廃止した追加指示の `follow_up` も残る）。そのほか `settings`（開始時に固定した設定）、`model`、`effort`、`usage`、`warnings`、
`notifyDecision`、`nextWakeAt`、`attempts[]`（`phase`: `preparing` / `sending` / `running` / `finished` / `failed` /
`cancelled` / `unknown` / `abandoned`）。

### 常駐の通知

```json
{"type": "resident", "taskId": "task_…", "runId": "run_…", "notificationId": "ntf_…",
 "kind": "change", "title": "CI監視", "body": "CIが復旧しました", "gatewayId": "…"}
```

`kind` は `change` / `question` / `failure`。同じ `notificationId` は同じ通知の再送で、アプリは二度表示しない。
