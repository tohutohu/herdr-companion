# hc — エージェント向け CLI

`hc`（`gateway/cmd/hc`）は、Herdr 上の Claude Code / Codex / OpenCode / Devin セッションを
別の AI エージェントが見張り、答え、指示するための CLI。Gateway の HTTP API
（[api.md](api.md)）のクライアントで、Gateway 側の変更は要らない。

ヘルプはエージェントスキルと同じく段階的に開示する。エージェントには「まず `hc guide` を読んで」と伝えれば足りる。

| 層 | コマンド | 内容 |
|---|---|---|
| 1 | `hc help`（引数なしの `hc`） | 用途（使う場面・使わない場面）、クイックスタート、用途別のコマンド索引とトピック一覧 |
| 2 | `hc help COMMAND`（`hc COMMAND --help`） | 1 コマンドの USE WHEN / USAGE / FLAGS / OUTPUT / EXAMPLES / SEE ALSO |
| 2 | `hc help TOPIC` | `states` `ids` `cursors` `timeouts` `connect` `output` の概念説明 |
| 3 | `hc guide` | 常駐して見張るエージェント向けの手順書（委任・起動・並列・監視・回答・端末操作） |

ヘルプの表示には接続設定が要らない。コマンドラインの誤りは該当コマンドのヘルプページ名を添えて終了コード 2 で返す。

## インストール

GitHub Release に各プラットフォームのバイナリが添付される（`hc version` でリリースのバージョンを表示）。
asset 名にバージョンを含まないので、次の URL で常に最新版を取れる。

```bash
# macOS (Apple Silicon)。ほかは hc-darwin-amd64 / hc-linux-amd64 / hc-linux-arm64 / hc-windows-amd64.exe / hc-windows-arm64.exe
curl -fsSL -o ~/.local/bin/hc https://github.com/tohutohu/herdr-companion/releases/latest/download/hc-darwin-arm64
chmod +x ~/.local/bin/hc
```

macOS のバイナリは署名していない。curl で取れば quarantine 属性は付かないが、ブラウザで落とした場合は
`xattr -d com.apple.quarantine hc` が要る。ソースからは次のとおり。

```bash
cd gateway
env -u GOROOT go build -o ~/.local/bin/hc ./cmd/hc
```

この Mac ではそのまま動く。接続先とトークンは Gateway の設定
（`HERDR_MOBILE_CONFIG`、なければ `~/.config/herdr-mobile/desktop/config.json`）から読む。

### Linux / Windows から

依存は標準ライブラリだけ（cgo なし）なので、リリースの単体バイナリを置けば動く。

- `HC_URL`（Tailscale の `http://100.99.15.34:8765` か Cloudflare Tunnel の URL）と `HC_TOKEN` を設定する。
- `hc start` の `--cwd` は必須（Gateway の Mac 上のパスを渡す。手元のカレントディレクトリは使わない）。
- `--file` の添付はアップロードされるので、手元のファイルを渡してよい。`export` も手元に書き出す。
- 状態は Linux では `~/.local/state/herdr-mobile/hc`、Windows では `%LOCALAPPDATA%\herdr-mobile\hc`。
- Windows の標準入力の CRLF は LF にして送る。出力は UTF-8（`⋯` `→` などを含む）なので、古いコードページのコンソールでは化けることがある。
- Linux（amd64 / arm64）はコンテナでテストを実行して確認済み。Windows はビルドのみ確認。

## 設計

| 方針 | 内容 |
|---|---|
| 出力はモデルが読む前提 | 既定は短いテキスト。1 セッション 1 行、ツール呼び出しは「⋯ 7 tool calls, latest: …」の 1 行に畳む。長い本文は切り、末尾に全文を出すコマンドを付ける。`--json` で Gateway のオブジェクトそのもの |
| 差分で読む | `read` / `changes` / `wait` は読み手（カーソル）ごとに「どこまで見せたか」を覚え、次は新着だけを出す。`--peek` はカーソルを動かさない |
| 待つのは CLI 側 | `wait` は対応が要る変化（質問・承認・ターン終了）まで、`until` は指定セッションが指定の状態になるまで内部でポーリングして返る。エージェントがループで `ls` を繰り返してトークンを使わずに済む |
| 次の一手を示す | 保留中の質問・承認には、そのまま実行できる `hc answer …` を添える |
| 誤操作を手前で止める | 回答は選択肢と照合してから送る（番号・ラベル・自由入力を受け付け、合わなければ選択肢を表示）。自分自身のセッションへの送信・アーカイブは拒否する |
| 大きいものはファイルへ | `export` は会話全体を Markdown に書き出してパスだけを表示する。grep や部分読みに使う |

### カーソルと自分自身

- 状態は `~/.local/state/herdr-mobile/hc/<カーソル名>/` に置く（`HC_STATE_DIR` で変更）。
  `sessions.json` がセッション一覧のスナップショット、`reads/<セッション>.json` がセッションごとの既読位置。
- カーソル名は既定で Herdr のペイン（`HERDR_PANE_ID`）ごと。`HC_CURSOR` か `--cursor` で共有・分離できる。
- `HERDR_PANE_ID` のペインで動いているセッションは呼び出し元自身とみなし、変化として報告しない（一覧では `(you)`）。

### 変化の判定

- `changes` / `wait` はスナップショットとの差分を出す。初回は基準を保存し、回答待ちのセッションだけを報告する。
- 対応が要る（`!`）のは、`waiting_input` / `waiting_approval` / `completed` / `failed` になったとき、
  `running` から `idle` / `offline` になったとき、同じ状態のまま新しくなったとき（ポーリングの間に 1 ターン終わった）。
- 停止中（offline）のセッションが一覧に出入りするのは変化として扱わない。
- `wait ID…` は指定セッションだけを待ち、ほかのセッションの変化はカーソルに残す。指定セッションが既に回答待ちなら即座に返る。
- `send` / `answer` / `start` の `--wait` は、そのターンの終わりまで待って新着を表示する。
  短いターンは `running` が見えないまま `idle` に戻ることがある（Herdr はフォーカス中のペインを `completed` にしない）ため、
  操作後にエージェントの発言が増えたことでも終わりとみなす。

### 状態を待つ（`until`）

`wait` が「前回見たときからの変化」を待つのに対し、`until ID…` は指定セッションの**今の状態**を見て、
`--for` の状態になるまで待つ。既にその状態なら即座に返る（`already there:`）。

| `--for` | 状態 |
|---|---|
| `input` / `approval` / `needs-you` | `waiting_input` / `waiting_approval` / その両方 |
| `done` | `completed` か `idle`（ターン終了） |
| `failed` / `offline` / `running` | それぞれの状態。アーカイブされて一覧から消えたセッションは `offline` |
| `stopped`（既定） | `running` 以外のすべて |

- 生の状態名（`idle` など）も受け付け、完全一致で判定する。カンマ区切りか繰り返しで複数指定できる。
- `hc send` の直後に呼んでもよい。`done` と `stopped` は、最新のメッセージが 1 分以内の指示（エージェントがまだ着手していない）なら `idle` / `completed` を終わりとみなさない。中断されたターンで待ち続けないよう、1 分を過ぎた指示は見ない。
- 複数 ID は最初の 1 件が着いた時点で返る。`--all` で全件を待つ。
- 到達したセッションは保留中の質問と回答コマンド、または最後の発言を表示し、カーソルもそのセッション分進める（後の `wait` で同じ変化を再報告しない）。
- 時間切れは終了コード 3。各セッションの現状と、待ち続けるための `continue: hc until …` を表示する。
- `send` / `answer` / `start` の `--wait` が時間切れのときの案内も `hc until ID` にした。

### シェルのタイムアウト

`wait` / `until` の既定 `--timeout` は 110 秒で、Claude Code の Bash の既定タイムアウト（2 分）に収まる。
長く待つときは `--timeout 9m` と Bash の `timeout: 600000` を組み合わせるか、バックグラウンドで実行する。
`wait` の時間切れは正常終了で、「nothing needs you yet」と動作中のセッションを表示する。`until` の時間切れは終了コード 3。

## 例

```text
$ hc ls
! a9b7233a waiting_input      now  claude proj · 色の選択ツール
  5b27a61d running            now  claude herdr-android-client · AIエージェント向けCLI設計  (you)
(+47 offline: hc ls --all)

$ hc wait --timeout 9m
! a9b7233a idle → waiting_approval  proj · 色の選択ツール
    [approval PENDING] Allow Write?
      …/proj/hello.txt
      answer: hc answer a9b7233a approve | deny
next: hc read a9b7233a   (or hc show ID)

$ hc send 5b27 "テストを実行して"
sent to 5b27
follow up: hc until 5b27   then: hc read 5b27

$ hc until 5b27 --timeout 9m
reached after 1m12s:
  5b27a61d idle               now  claude proj · テスト実行
    last: テストは 42 件すべて通りました。
next: hc read 5b27a61d

$ hc answer a9b7 approve --wait
answered a9b7: approve
a9b7 idle · proj · 色の選択ツール
   ⋯ 1 tool call, latest: Write hello.txt
── assistant 17:15 · c36a1f47
hello.txt ファイルを作成しました。
[file hello.txt]
```
