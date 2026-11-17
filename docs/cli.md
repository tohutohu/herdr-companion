# hc — エージェント向け CLI

`hc`（`gateway/cmd/hc`）は、Mac 上の Claude Code / Codex セッションを
別の AI エージェントが見張り、答え、指示するための CLI。Gateway の HTTP API
（[api.md](api.md)）のクライアント。セッションはすべて Gateway 自身が動かす（Herdr 不要）。

ヘルプはエージェントスキルと同じく段階的に開示する。エージェントには「まず `hc guide` を読んで」と伝えれば足りる。

| 層 | コマンド | 内容 |
|---|---|---|
| 1 | `hc help`（引数なしの `hc`） | 用途（使う場面・使わない場面）、クイックスタート、用途別のコマンド索引とトピック一覧 |
| 2 | `hc help COMMAND`（`hc COMMAND --help`） | 1 コマンドの USE WHEN / USAGE / FLAGS / OUTPUT / EXAMPLES / SEE ALSO |
| 2 | `hc help TOPIC` | `states` `ids` `cursors` `timeouts` `connect` `output` の概念説明 |
| 3 | `hc guide` | 常駐して見張るエージェント向けの手順書（委任・起動・並列・監視・回答・モデル変更・中断） |

## コマンド

| 分類 | コマンド |
|---|---|
| 見る | `ls` `search` `show` `read` `export` |
| 待つ | `until` `wait` `changes` |
| 操作 | `send` `answer` `start` `archive` `resume` |
| 設定 | `model` `effort` `mode` `interrupt` |
| メモ | `note`（`ls` `show` `add` `edit` `rm` `clear`） |
| プロジェクト | `fav`（`ls` `add` `rm` `mv`） |
| 常駐 | `resident`（`ls` `show` `create` `update` `preview` `pause` `resume` `run` `runs` `log` `cancel` `dismiss` `promote` `send` `answer` `messages` `memory` `delete`） |
| その他 | `usage` `guide` `version` `update` |

- `hc search WORD...` はアーカイブ済みも含めてセッションを探す。すべての語がタイトル・プロジェクト・フォルダ・ID・最後のメッセージのどこかに含まれるものを新しい順に出す。シェルで引用符に囲んだ引数（`hc search "login bug"`）はフレーズとして探す。`--text` で会話の本文も対象にする。`--active` / `--archived` で片方に絞り、`--limit N`（既定 20、0 で全件）で件数を変える。検索は Gateway の索引（`GET /v1/sessions/search`）で行い、対応前の Gateway には `hc` が一覧と会話を読んで自分で探す（`--text` は時間がかかる）。
- `hc start` は Gateway が起動し、すぐにセッション ID を返す（`started claude:<id>`）。`--provider` は `claude`（既定）/ `codex` / `dsh`。
- `hc start --translation` は翻訳モードで起動する。プロンプトは Gateway の翻訳で英語にして送られ、英語の返答はアプリで日本語に訳して表示される。付けなければ Gateway の既定のまま。
- `hc model ID MODEL` / `hc effort ID LEVEL` はセッション ID と履歴を保ったまま変える。カタログに無い値は理由付きで拒否され、別の値には置き換わらない。Claude Code は次のモデル呼び出しから、Codex は次のターンから効き、それまで `hc show` に「switching to …」と出る。
- `hc mode ID [MODE]` は巡回、または直接指定。`hc interrupt ID` は実行中のターンだけを止める。
- `hc resume ID` は停止中（offline）・アーカイブ済みのセッションを、どこで始めたものでも同じ ID のまま新しいプロセスで再開する。Gateway が一覧で動作中と出すセッションはプロセスが止まっていても `hc send` だけで再開する。
- `hc answer` で答えられない要求（`not supported here`）は、`hc interrupt` でターンを止めて次の指示を送る。

### メモ（`hc note`）

セッションに書き置きを残し、そのセッションを見張るエージェント（`hc wait` で監視するもの、常駐タスク）に扱いを伝える。
例: 「PR にレビューが来たら対応させて」「ずっと待機させておく」「PR が何事もなくマージされたらアーカイブしてよい」。
Gateway が保存する（[api.md](api.md#メモ)）ので、どの端末・どのエージェントからも同じメモが見える。セッション自身のエージェントには送られない。

```bash
hc note add a9b7 "PRにレビューが来たら対応させる。マージされたらアーカイブしてよい"
hc note add self --ttl 2d "PR #42 を出した。CI が落ちたら直させて"   # 自分のセッションに。--ttl で期限
hc note ls                     # アーカイブしていない全セッションのメモ（--archived でアーカイブ済みも）
hc note ls a9b7                # 1 セッション分
hc note edit n3f9 "ずっと待機させておく"   # NOTE は n+8 桁の ID か一意な前方一致。--ttl / --no-expiry で期限
hc note rm n3f9                # 対応し終えたら消す。hc note clear ID で全部
```

- `hc ls` / `search` / `show` / `changes` / `wait` / `until` は各セッションの下にメモを全文で出す（`note n3f9a01c2 (2h ago, by claude:5b27a61d, expires in 2d): …`、複数行のメモは次の行から字下げ）。監視役は `hc ls` や `hc wait` だけで、反応する変化と一緒に指示を読める。
- `hc ls` は、停止中（offline）でもメモのあるセッションは隠さずに出す。
- 書いたエージェントは `HC_SESSION_ID` などから分かる自分のセッション ID で記録される（`by …`）。
- メモは指示であってロックではない。対応したら消すか書き換える。
- メモに対応しない Gateway では「update the Gateway」と出て終了コード 1。
- アプリ（Android・Mac UI）ではセッション画面の入力欄の ＋ メニュー（停止中のセッションは ⋮ メニュー）の「Notes」から、同じメモを見る・書く・書き換える・消すことができる。アプリから書いたメモは `by` なし（人が書いたもの）になる。

### お気に入りのフォルダ（`hc fav`）

ユーザーがプロジェクトとして使うフォルダのお気に入り。Gateway が並び順ごと保存し（[api.md](api.md#お気に入りのフォルダ)）、
アプリの新規セッション画面と同じ一覧が出る。エージェントが起動先のプロジェクトを選ぶときに使う。

```bash
hc fav                          # 1  bot  /Users/…/workspace/bot のように番号・フォルダ名・パス
hc start --cwd herdr-android-client "…"   # --cwd にお気に入りのフォルダ名（今のディレクトリに同名のフォルダが無いとき）
hc fav add ~/workspace/new-app --at 1     # 起動できるフォルダ（workspaceRoots の中）だけ。省略で今のディレクトリ、末尾
hc fav rm 3                     # 番号・一意なフォルダ名・パスで指定
hc fav mv new-app 2             # 並べ替え
```

- `hc resident create/update --cwd` も同じくお気に入りのフォルダ名を受け付ける。
- お気に入りに対応しない Gateway では「update the Gateway」と出て終了コード 1。

### 常駐タスク（`hc resident`）

Gateway が予定どおり実行する常駐タスク（[resident.md](resident.md)）を管理する。セッションではないので `hc ls` には出ない（`promote` した Run を除く）。
詳細は `hc help resident`。
権限は既定のまま（`workspace_write`: Claude は auto モードで普通のコマンド・編集・Web を分類器が通す）でよく、
念のための `--profile read_only`・細かい `--allow`・指示文での禁止は、ユーザーが求めたときだけ付ける。

```bash
hc resident create --provider claude --model haiku --cwd ~/workspace/app \
  --instruction-file ci.md --weekly mon,thu@07:00 --trust   # 名前は指示の1行目、信頼していないフォルダは --trust
hc resident preview --cron "0 7 * * 1-5"            # 次の3回（保存しない）
hc resident ls                                      # "!" は判断待ち・要確認
hc resident run CI監視                              # 今すぐ実行（待機中の Run があればまとめる）
hc resident answer CI監視 "マージしないで報告だけ"   # 判断待ちへの回答
hc resident runs CI監視 && hc resident log RUN       # 実行履歴と 1 件の詳細
hc resident cancel RUN                              # その Run だけを停止
hc resident promote RUN                             # その Run の会話をセッションにして続ける（hc send で話しかける）
hc resident memory CI監視 --set memory.json         # {summary?, state?} で Memory を置き換え
hc resident update 物件 --precheck 'python3 scrape.py'  # 事前チェック: exit 0 のときだけエージェントを起動（'' で外す）
```

- TASK はタスク ID、その一意な前方一致、または名前。`--file task.json` で API と同じ JSON を渡せ、フラグが上書きする。
- 作成・手動実行・回答は requestId を付けて送る（`--request-id` で固定すれば再試行しても増えない）。
- 更新は現在の版（`expectedVersion`）を自動で付ける。ほかで変更されていれば `conflict` で失敗する。
- `--json` は API の JSON をそのまま出す。終了コードは 0 成功、1 拒否・失敗（理由を stderr に出す）、2 コマンドラインの誤り。
- 常駐に対応しない Gateway では「update the Gateway」と出て終了コード 1。
- エージェントが使える外部連携は、この Mac で設定済みの CLI / MCP だけ。hc 自体はメールや Web 検索を提供しない。

2.0 で `term` / `keys`（Herdr のペインの画面とキー入力）、`migrate`（Herdr → Gateway）、`--backend` とペイン ID での指定はなくなった。

ヘルプの表示には接続設定が要らない。コマンドラインの誤りは該当コマンドのヘルプページ名を添えて終了コード 2 で返す。

## インストール

GitHub Release に各プラットフォームのバイナリが添付される（`hc version` でリリースのバージョンを表示）。
asset 名にバージョンを含まないので、次の URL で常に最新版を取れる。

```bash
# macOS (Apple Silicon)。ほかは hc-darwin-amd64 / hc-linux-amd64 / hc-linux-arm64 / hc-windows-amd64.exe / hc-windows-arm64.exe
curl -fsSL -o ~/.local/bin/hc https://github.com/tohutohu/herdr-companion/releases/latest/download/hc-darwin-arm64
chmod +x ~/.local/bin/hc
```

macOS のバイナリは Mac アプリと同じ自己署名証明書で署名している（[macos-release.md](macos-release.md#固定の証明書による署名)）。信頼された証明書ではないので、curl で取れば quarantine 属性は付かないが、ブラウザで落とした場合は
`xattr -d com.apple.quarantine hc` が要る。

一度入れたあとは `hc update` で最新のリリースに置き換えられる（Gateway への接続は要らない）。

```bash
hc update --check   # 新しいリリースがあるかだけ見る
hc update           # hc-<os>-<arch> を取り、SHA-256 と起動を確かめてから実行中の hc（シンボリックリンクは辿る）と入れ替える
```

- 照合には GitHub が返す asset の `digest`、なければ `.sha256` asset を使う。どちらも無い・合わない・`hc version` が期待の版を名乗らないときは何も置き換えない。
- ソースからビルドした hc（`hc version` が `dev`）は `--force` を付けたときだけ置き換える。`--force` は同じ版の入れ直しにも使える。
- hc のあるフォルダに書き込めないときは失敗する（`sudo` で入れた場合など）。
- macOS では、入っている hc が証明書で署名されていれば、同じ証明書で署名されたリリースしか入れない（ad-hoc 署名の hc はどのリリースにも置き換わる）。
- ソースから入れるときは `scripts/install-hc.sh`（同じ証明書で署名して `~/.local/bin/hc` に置く）。ソースからは次のとおり。

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
| 1 回で足りる | 一覧・待機・検索の結果に、そのセッションをどう扱うかの判断に要る情報（メモなど）を載せる。読み手が「詳細を見るためにもう 1 コマンド」を打たずに済むようにする |
| 出力はモデルが読む前提 | 既定は短いテキスト。1 セッション 1 行、ツール呼び出しは「⋯ 7 tool calls, latest: …」の 1 行に畳む。長い本文は切り、末尾に全文を出すコマンドを付ける。`--json` で Gateway のオブジェクトそのもの |
| 差分で読む | `read` / `changes` / `wait` は読み手（カーソル）ごとに「どこまで見せたか」を覚え、次は新着だけを出す。`--peek` はカーソルを動かさない |
| 待つのは CLI 側 | `wait` は対応が要る変化（質問・承認・ターン終了）まで、`until` は指定セッションが指定の状態になるまで内部でポーリングして返る。エージェントがループで `ls` を繰り返してトークンを使わずに済む |
| 次の一手を示す | 保留中の質問・承認には、そのまま実行できる `hc answer …` を添える |
| 誤操作を手前で止める | 回答は選択肢と照合してから送る（番号・ラベル・自由入力を受け付け、合わなければ選択肢を表示）。自分自身のセッションへの送信・アーカイブは拒否する |
| 大きいものはファイルへ | `export` は会話全体を Markdown に書き出してパスだけを表示する。grep や部分読みに使う |

### カーソルと自分自身

- 状態は `~/.local/state/herdr-mobile/hc/<カーソル名>/` に置く（`HC_STATE_DIR` で変更）。
  `sessions.json` がセッション一覧のスナップショット、`reads/<セッション>.json` がセッションごとの既読位置。
- カーソル名は既定でセッションごと。`HC_CURSOR` か `--cursor` で共有・分離できる。
- 呼び出し元自身のセッションは、Gateway が動かすエージェントに渡す `HC_SESSION_ID`、またはエージェント自身が
  ツールに渡す `CLAUDE_CODE_SESSION_ID`（Claude Code）・`CODEX_THREAD_ID`（Codex）で判定する。自分自身は変化として報告せず（一覧では `(you)`）、送信・アーカイブ・中断・待機を拒否する。

### 変化の判定

- `changes` / `wait` はスナップショットとの差分を出す。初回は基準を保存し、回答待ちのセッションだけを報告する。
- 対応が要る（`!`）のは、`waiting_input` / `waiting_approval` / `completed` / `failed` になったとき、
  `running` から `idle` / `offline` になったとき、同じ状態のまま新しくなったとき（ポーリングの間に 1 ターン終わった）。
- 停止中（offline）のセッションが一覧に出入りするのは変化として扱わない。
- `wait ID…` は指定セッションだけを待ち、ほかのセッションの変化はカーソルに残す。指定セッションが既に回答待ちなら即座に返る。
- `send` / `answer` / `start` の `--wait` は、そのターンの終わりまで待って新着を表示する。
  短いターンは `running` が見えないまま `idle` に戻ることがある（終わったターンは誰かが見ると `completed` ではなく `idle` になる）ため、
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
| `settled` | `stopped` かつ裏の作業も終わった状態（回答待ちなら裏の作業を待たない） |

- 生の状態名（`idle` など）も受け付け、完全一致で判定する。カンマ区切りか繰り返しで複数指定できる。
- `hc send` の直後に呼んでもよい。`done` と `stopped` は、最新のメッセージが 1 分以内の指示（エージェントがまだ着手していない）なら `idle` / `completed` を終わりとみなさない。中断されたターンで待ち続けないよう、1 分を過ぎた指示は見ない。
- 複数 ID は最初の 1 件が着いた時点で返る。`--all` で全件を待つ。
- 到達したセッションは保留中の質問と回答コマンド、または最後の発言を表示し、カーソルもそのセッション分進める（後の `wait` で同じ変化を再報告しない）。
- `completed` / `idle` でも、セッションが始めたサブエージェントやコマンドが裏で動いていることがある。`ls` などに `[N in background]`、`show` に一覧、`--json` に `background` が出る。Claude Code は終わると自動で新しいターンを始めるので、結果まで待つなら `settled` を使う。終わった直後のターン開始を見逃さないよう、2 回続けて落ち着いていたら到達とする。開発サーバーのように終わらないものがあると `settled` にならないので、`--timeout` を付ける。Codex の裏の作業と、エージェントが `nohup` や `&` で切り離したコマンドは数えない。
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

$ hc start --cwd ~/workspace/app --provider codex "テストを直して"
started codex:01a18a22-1465-7690-8177-db1bb2d0f68f

$ hc model 9bd45e93 sonnet
model: claude-haiku-5-5 → sonnet from the next model request

$ hc answer a9b7 approve --wait
answered a9b7: approve
a9b7 idle · proj · 色の選択ツール
   ⋯ 1 tool call, latest: Write hello.txt
── assistant 17:15 · c36a1f47
hello.txt ファイルを作成しました。
[file hello.txt]
```
