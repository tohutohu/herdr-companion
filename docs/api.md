# Herdr Companion Gateway API

Mac 上の Gateway（`gateway/`）が提供する HTTP API の仕様と、Mac の外（Android・
他端末・スクリプト）から接続するための情報をまとめる。実装の正は
`gateway/internal/api/server.go` と `gateway/internal/model/`。設計の背景は
[architecture.md](architecture.md) を参照。

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
| 409 | セッションが Herdr で動いていない／既に動いている、質問が既に閉じた、作業ディレクトリが無い、同名フォルダが既にある、エージェントがブロック中 |
| 413 | ファイル・アップロードが大きすぎる |
| 422 | そのプロバイダーが対応していない操作 |
| 502 | Herdr ソケット API のエラー |
| 503 | エージェント更新・セッション起動機能が無効 |

### セッション ID

`<provider>:<native-id>` 形式。`provider` は `claude` / `codex` / `opencode` / `devin`。
`native-id` は各エージェント自身のセッション（スレッド）ID。`paneId` は「今どこで動いているか」で、識別子ではない。

### セッションの状態（`status`）

| 値 | 意味 |
|---|---|
| `running` | 作業中 |
| `waiting_input` | 質問への回答待ち |
| `waiting_approval` | 承認待ち（プランの承認を含む） |
| `completed` | 最後のターンが完了 |
| `idle` | 待機中 |
| `failed` | 最後のターンが失敗 |
| `offline` | Herdr 上で動いていない（直近 `offlineSessionDays` 日、既定 3 日以内に更新されたもの） |

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
| POST | `/v1/sessions/{id}/respond` | 要 | 質問・承認への回答 |
| POST | `/v1/sessions/{id}/mode` | 要 | TUI のモードを次へ切り替え |
| POST | `/v1/sessions/{id}/seen` | 要 | 既読にする |
| POST / DELETE | `/v1/sessions/{id}/archive` | 要 | アーカイブ／解除 |
| POST | `/v1/sessions/archive` | 要 | 一括アーカイブ |
| POST | `/v1/sessions/{id}/resume` | 要 | 停止中のセッションを再開 |
| GET | `/v1/sessions/{id}/messages/{mid}/images/{n}` | 要 | メッセージ内の画像 |
| GET | `/v1/sessions/{id}/files` | 要 | ディレクトリ一覧 |
| GET | `/v1/sessions/{id}/files/content` | 要 | ファイル本体 |
| GET | `/v1/sessions/{id}/files/stat` | 要 | ファイル情報 |
| GET / POST | `/v1/sessions/{id}/terminal` | 要 | ペインのテキスト読み取り／キー入力 |
| POST | `/v1/launches/{pane}/trust` | 要 | 起動時のフォルダ信頼ダイアログに回答 |
| POST | `/v1/launches/{pane}/continue` | 要 | 手動操作後に起動を再開 |
| GET / POST | `/v1/launches/{pane}/terminal` | 要 | セッション ID 確定前のターミナル |
| GET | `/v1/models` | 要 | 起動時に選べるモデル・effort・モード |
| GET / POST | `/v1/directories` | 要 | 作業フォルダの一覧／作成 |
| POST | `/v1/directories/check` | 要 | 作業フォルダの事前チェック（Jev） |
| GET | `/v1/agents` | 要 | インストール済み CLI のバージョンと更新状況 |
| POST | `/v1/agents/{provider}/update` | 要 | CLI の更新を開始 |
| POST | `/v1/uploads` | 要 | 添付ファイルのアップロード |
| GET | `/v1/usage` | 要 | サブスクリプション利用枠（キャッシュ） |
| POST | `/v1/usage/refresh` | 要 | 利用枠を今すぐ読み直す |
| POST / DELETE | `/v1/devices` | 要 | FCM トークンの登録／解除 |

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
  "paneId": "<Herdr のペイン ID>",
  "model": "claude-opus-5-5",
  "effort": "high",
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
| `canSend` | 今メッセージを送れるか（Herdr 上で動いているか） |
| `unread` | エージェントが完了し、そのペインがまだフォーカスされていない |
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

新しい Herdr ワークスペースでエージェントを起動する。`cwd` は `workspaceRoots`（未設定ならホーム）配下に限る。

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
  "worktree": false
}
```

| フィールド | 必須 | 説明 |
|---|---|---|
| `provider`, `cwd` | ○ | |
| `prompt` | | 最初のプロンプト。`uploads` は最初のプロンプトに添付される |
| `model` / `effort` / `mode` | | `GET /v1/models` の `id`。空ならエージェントの既定 |
| `trust` | | フォルダ信頼ダイアログを代わりに承認する。`false` だとダイアログで止まり `trustRequired` を返す |
| `worktree` | | `cwd` のリポジトリに Herdr が新しい git worktree を作り、そこで起動する |

`201`（StartResult）:

```json
{ "sessionId": "claude:…", "paneId": "<Herdr のペイン ID>", "warning": "…", "trustRequired": false }
```

`sessionId` はエージェントが ID を報告するまで省略されることがある。その間は
`/v1/launches/{pane}/terminal` でペインを操作できる。

### `POST /v1/launches/{pane}/trust`

`trustRequired` で止まった起動に回答する。`{"trust": true}` で続行、`false` でワークスペースを閉じる。
`200` で StartResult。保留中の起動がなければ `404`。

### `POST /v1/launches/{pane}/continue`

ターミナルで手動操作した後、起動準備の確認をやり直し、保持していた最初のプロンプトを 1 度だけ送る。`200` で StartResult。

### `GET /v1/sessions/{id}`

Session を 1 件返す。

### `POST /v1/sessions/{id}/archive` / `DELETE /v1/sessions/{id}/archive`

POST: 動作中なら停止（Herdr のペインを閉じる）し、失うものがなければ起動時の worktree を削除してアーカイブする。
レスポンスは Session に `warning`（worktree を残した理由など）を加えたもの。
DELETE: アーカイブを解除し Session を返す。

### `POST /v1/sessions/archive`

```json
{ "ids": ["claude:…", "codex:…"] }
```

重複は無視、空文字は `400`。`{"sessions": [Session + warning?, …]}`。

### `POST /v1/sessions/{id}/resume`

停止中のセッションを新しい Herdr ワークスペースで再開し、アーカイブから外す。ボディは省略可（`{"trust": true}`）。
`201` で StartResult。既に動いていれば `409`。

## メッセージ

### `GET /v1/sessions/{id}/messages?after=<message id>`

```json
{ "session": Session, "messages": [Message, …] }
```

- `after` を付けると、そのメッセージ以降（そのメッセージ自身を含む）だけを返す。進行中の更新を拾うため。未知の ID なら全件。
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
    { "type": "interaction", "interaction": Interaction }
  ]
}
```

- `role`: `user` / `assistant` / `tool` / `system`。
- `queued`: エージェントが受け取ったがまだ会話に取り込んでいないプロンプト（Claude Code の作業中キュー）。
- Block は `type` ごとに該当フィールドだけが入る平坦な union。未知の `type` は `text` として描画すること。
- 画像の `url` は Gateway 相対。ベース URL を前に付け、Bearer トークン付きで取得する。

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
| `supported` | `false` なら表示のみ。回答はターミナル経由で行う |
| `questions[].type` | `select` / `multiselect` / `text` |

### `POST /v1/sessions/{id}/messages`

```json
{ "text": "続けて", "uploads": ["<upload id>"] }
```

テキストも添付も空なら `400`。`202 {"ok": true}`。リクエストを受けた後はクライアントが切断しても送信を完了させる（最大 2 分）。

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

動作中の TUI のモード（Plan、Accept edits など）を次に進める。キー操作の選択はプロバイダーごとに Gateway が行う。`202`。非対応は `422`。

### `POST /v1/sessions/{id}/seen`

Herdr で `done` のセッションならペインをフォーカスして既読にする（それ以外は何もしない）。`200 {"ok": true}`。

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

## ターミナル

構造化 API で扱えない画面（未対応の質問など）を操作するためのフォールバック。

### `GET /v1/sessions/{id}/terminal?lines=200`

`lines` は 1〜2000（既定 200）。動作中でなければ `409`。

```json
{ "paneId": "<Herdr のペイン ID>", "text": "…", "revision": 123 }
```

### `POST /v1/sessions/{id}/terminal`

```json
{ "text": "y", "keys": ["enter"] }
```

`text` を先に送り、次に `keys` を送る。`202 {"ok": true}`。使えるキー:

`enter` `esc` `tab` `shift+tab` `space` `backspace` `up` `down` `left` `right` `ctrl+c` `ctrl+d` `1`〜`9` `y` `n`

### `/v1/launches/{pane}/terminal`（GET / POST）

形式は上と同じ。セッション ID が確定する前の起動中ペイン用で、この Gateway が起動したペインだけを操作できる。

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

`state` は `idle` / `running` / `succeeded` / `failed`。対象は `claude` / `codex` / `opencode` / `devin`。

### `POST /v1/agents/{provider}/update`

更新コマンド（`claude update`、`codex update`、`opencode upgrade`、`devin update`）を開始する。`202` で上と同じ要素 1 件。
実行中に繰り返し呼ぶと同じジョブを共有する。未知のプロバイダーは `400`。

## アップロード

### `POST /v1/uploads`

ボディはファイルの生バイト列。`Content-Type` にファイルの型、任意で
`Content-Disposition: attachment; filename="a.png"` で名前を付ける（無ければ型から拡張子を決める）。
上限 20 MiB（超えると `413`）。24 時間後に削除される。

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

## 通知（FCM）

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
  "project": "herdr-android-client",
  "canSend": "true"
}
```
