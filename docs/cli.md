# hc — エージェント向け CLI

`hc`（`gateway/cmd/hc`）は、Herdr 上の Claude Code / Codex / OpenCode / Devin セッションを
別の AI エージェントが見張り、答え、指示するための CLI。Gateway の HTTP API
（[api.md](api.md)）のクライアントで、Gateway 側の変更は要らない。

使い方の全体は `hc help`、エージェント向けの手引きは `hc guide` が出力する。
エージェントには「まず `hc guide` を読んで」と伝えれば足りる。

## インストール

```bash
cd gateway
env -u GOROOT go build -o ~/.local/bin/hc ./cmd/hc
```

この Mac ではそのまま動く。接続先とトークンは Gateway の設定
（`HERDR_MOBILE_CONFIG`、なければ `~/.config/herdr-mobile/desktop/config.json`）から読む。

### Linux / Windows から

依存は標準ライブラリだけ（cgo なし）なので、クロスコンパイルした単体バイナリをコピーすれば動く。

```bash
GOOS=linux   GOARCH=amd64 env -u GOROOT go build -o hc            ./cmd/hc   # arm64 も可
GOOS=windows GOARCH=amd64 env -u GOROOT go build -o hc.exe        ./cmd/hc
```

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
| 待つのは CLI 側 | `wait` は対応が要る変化（質問・承認・ターン終了）まで内部でポーリングして返る。エージェントがループで `ls` を繰り返してトークンを使わずに済む |
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

### シェルのタイムアウト

`wait` の既定 `--timeout` は 110 秒で、Claude Code の Bash の既定タイムアウト（2 分）に収まる。
長く待つときは `--timeout 9m` と Bash の `timeout: 600000` を組み合わせるか、バックグラウンドで実行する。
時間切れは正常終了で、「nothing needs you yet」と動作中のセッションを表示する。

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

$ hc answer a9b7 approve --wait
answered a9b7: approve
a9b7 idle · proj · 色の選択ツール
   ⋯ 1 tool call, latest: Write hello.txt
── assistant 17:15 · c36a1f47
hello.txt ファイルを作成しました。
[file hello.txt]
```
