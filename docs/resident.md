# 常駐タスク（Resident tasks）

継続して任せるタスクを Gateway が予定どおり実行し、前回の状態を覚え、必要な報告だけを返す機能。
Android と Mac UI（Compose Desktop）には Sessions と並ぶトップレベルの「常駐」タブがあり、
作成・実行・報告確認・停止まで常駐側だけで完結する。報告の続きを話したいときは、その Run の会話をセッションにして続ける。`hc resident` からも管理できる（[cli.md](cli.md)）。
API は [api.md](api.md#常駐タスク)。実装は `gateway/internal/resident`。

例:「毎朝このリポジトリを確認して、対応すべき変化があれば知らせて」「結果が未確定なら2時間後にもう一度確認して」。
「毎時サイトをスクレイピングして、条件に合う新着があったときだけ LLM に精査させる」なら[事前チェック](#事前チェック)を使う。

## 通常セッションとの関係

| | 通常セッション | 常駐タスク |
|---|---|---|
| 正本 | エージェントの履歴（Claude transcript / Codex rollout / DSH history） | Gateway の `resident.db` |
| 実行 | `internal/runtime.Manager`、`direct-sessions.json` | `internal/resident`（Run ごとに runId） |
| Claude | 共有 sidecar の `query()`（`persistSession: true`） | **Run ごとに起動する sidecar** の `query()`（`persistSession: true`, `outputFormat`） |
| Codex | Gateway の app-server のスレッド | **Run ごとに起動する app-server** のスレッド（`ephemeral: false`, `outputSchema`） |
| DSH | セッション専用 SDK プロセス | **Run ごとに起動する SDK プロセス**（`session/wait`、最終JSON検証） |
| 通知 | Watcher が状態遷移から送る | 結果確定時に Outbox へ入れ、Outbox から送る |

- 常駐の Run は Manager・`direct-sessions.json`・Watcher・セッション listener に登録されない。Run の会話（Claude transcript /
  Codex rollout / DSH history と表示用 journal）は[セッションに昇格](#セッションへの昇格)できるように残すが、`/v1/sessions` の offline 一覧は昇格していない
  Run の native id を除外する（`hc ls` も同じ一覧を使う）。
- 共有しているのは低層部分だけ: sidecar の stdio プロトコル（`startSidecar`）、Codex の JSON-RPC クライアントと stdio transport、
  Claude CLI のパス・ログイン・課金ガード（`billingProblem`、API キーの除去）、環境変数の整理（`ChildEnv`）、
  モデルカタログ（`/v1/models` と同じ）、workspaceRoots と信頼確認、FCM 送信部品、ファイルのパス検証。
- Run ごとに専用プロセスを起動するのは、Codex の `turn/interrupt` / `thread/unsubscribe` がエージェントの実行中コマンドを
  止めないため（コマンドは別プロセスグループで残る）。Run が終わる・停止される・時間切れになると、その sidecar / app-server と
  そこから起動されたプロセス（別グループのコマンドも含む）を `ps` の親子関係で列挙して終了する。共有の sidecar / app-server、
  通常セッション、他の Run、ユーザーの Herdr には触れない。
- エージェントの環境には `HC_RESIDENT_RUN=<runId>`（Claude）/`HC_RESIDENT=1`（Codex / DSH）が入り、`HC_SESSION_ID` は入らない。
  `herdr` や `hc` はツールとしてそのまま使える。

## データ（`resident.db`）

`config.StateDir()/resident.db`（既定 `~/.local/state/herdr-mobile/resident.db`、メニューバー版は `desktop/` 配下）。
SQLite（`modernc.org/sqlite`、純 Go で cgo 不要）、WAL、`synchronous=FULL`、ファイルは mode 600、接続は 1 本で直列化。
スキーマ版は `PRAGMA user_version`（現在 3）。起動時に古い版から順に移行し、新しすぎる版は開かずにエラーにする。

| テーブル | 内容 |
|---|---|
| `tasks` | 設定（JSON）、有効/一時停止と期限付き停止の終了時刻（`paused_until`）、版、信頼確認日時、削除日時、既読位置 |
| `memory` | 要約（≤8KiB）と構造化状態（JSON オブジェクト ≤64KiB, ≤256 キー）、版、更新者 |
| `messages` | タスクの会話（回答・報告・質問・お知らせ。以前の追加指示も残る）、処理した Run |
| `runs` | 状態、起動理由、開始時に固定した設定と Memory のスナップショット、結果、エラー種別、使用量、通知判断、会話の昇格状態（`conversation`, `session_id`） |
| `attempts` | Provider 呼び出し（段階、native id、保存済みの生の結果） |
| `wakeups` | 予定と自己予約（pending/fired/skipped/cancelled/merged） |
| `questions` / `artifacts` | 判断待ちの質問（期限付き）と成果物 |
| `notifications` / `notified_keys` | 通知 Outbox と通知済みキー |
| `requests` | requestId による冪等化（同じ ID で別内容なら 409） |

削除したタスクは論理削除（履歴は DB に残すが API からは見えない）。

## 予定

- 種類: 一回限り（`at`, RFC 3339）、毎日（`time`）、毎週（`days` + `time`）、cron（5 フィールド: 分 時 日 月 曜日。`*`・リスト・範囲・
  ステップ・月/曜日名、曜日 7 は日曜、日と曜日の両方指定はどちらかに一致で実行）、手動のみ。
- 時刻は UTC で保存し、繰り返しは IANA timezone（既定 `Asia/Tokyo`）の壁時計で解釈する。「毎朝 7 時」は 24 時間間隔ではない。
- 夏時間: 二度ある時刻は最初の 1 回だけ。存在しない時刻はずれの分だけ後ろへ（02:30 → 03:30）。同じ瞬間に重なれば 1 回。
- 有効なタスクは次回の予定 Wakeup を 1 件だけ持つ。予定の編集・一時停止・削除で旧 Wakeup は取り消され、起動しない。
  一時停止を解除すると「今」から次の予定を計算する（停止中の分は補完しない）。
- 一時停止には終了時刻を付けられる（例: 3 時間・明日の朝まで・1 週間。最長 366 日）。終了時刻を過ぎた最初の tick で
  自動的に再開し、次の予定は終了時刻から計算する。終了時刻より前の予定は起動しないが、終了後に Mac が眠っていて
  過ぎた予定は通常どおり補完の対象になる。手動で再開する・無期限で止め直す・設定で有効/無効を変えると終了時刻は消える。
- Gateway 停止中・スリープ中に過ぎた予定は、最新の 1 回が 24 時間以内（`residentCatchUpHours`）なら 1 回だけ補完実行し
  （起動理由「補完」）、それ以外はまとめて skipped の Run として残す。スケジューラは 30 秒ごとと変更時に壁時計を見るので、
  スリープ復帰後の最初の tick で処理される。時刻待ち・ポーリングに LLM は使わない。

## 実行

- 同じタスクは同時に 1 Run、全体は既定 1 Run（`residentMaxConcurrentRuns`）。手動・回答は定期実行より先に始める。
- 待機中の Run はタスクごとに最大 1 件。予定・自己予約・手動実行・回答が重なると、その Run に起動理由を追加してまとめる。
  回答のメッセージは捨てずに保存し、次に開始する Run が未処理分をまとめて扱う。
- 1 日の起動上限（既定 12、タスクの時刻帯の暦日）を超える**自動**起動は skipped（理由付き）。手動・回答は数えるが拒否しない。
- 1 Run の上限（既定 30 分）を超えると停止して timed_out。壁時計でも判定するのでスリープをまたいでも効く。
- 一時停止（期限付きも同じ）は自動起動だけを止める（手動実行・回答は実行する）。「今回の実行を停止」はその Run だけを中断・終了する。
  削除は待機中の Run と Wakeup を取り消し、実行中の Run を停止する。

### 事前チェック

タスクに `precheck`（シェルコマンド）を設定すると、自動起動（予定・補完・自己予約）の Run はエージェントより先にそれを実行し、
終了コードで LLM を起動するかを決める。スクレイピングや API 呼び出しのような安い確認を毎回 LLM に任せないための仕組み。

| 終了コード | 扱い |
|---|---|
| 0（条件に合う） | エージェントを起動する。標準出力（先頭 32 KiB）をプロンプトの「外部データ」に入れる |
| 1（条件に合わない） | エージェントを起動せずに Run を `skipped` で終える（会話・通知なし、`report` に標準出力）。1 日の起動上限に数えない |
| それ以外・時間切れ・起動できない | Run を失敗（`errorKind: precheck`）にし、標準エラーの末尾を残して失敗として通知する |

- `/bin/sh -c` でタスクのフォルダを作業ディレクトリにして実行する。時間制限は既定 60 秒（最長 600 秒、`maxRunMinutes` 未満）。
  終了・時間切れ・停止のときはプロセスグループごと終了する。Run の上限時間と停止操作もそのまま効く。
- 標準入力にタスクの Memory を `{"summary": "…", "state": {…}}` の JSON で渡す。エージェントが `statePatch` で残した既読 ID などと
  比べて、新しいものがあるときだけ exit 0 にできる。環境変数は `HC_RESIDENT=1`・`HC_RESIDENT_TASK`・`HC_RESIDENT_RUN`・
  `HC_RESIDENT_TRIGGERS`（カンマ区切りの起動理由）。PATH などは Gateway のもの。
- 手動実行・回答の Run は、結果にかかわらずエージェントを起動する（失敗は除く）。exit 1 のときはその旨と出力をプロンプトに伝える。
- コマンドはユーザーの権限で動き、権限プロファイルやエージェントの sandbox の対象外。設定できるのは Gateway のトークンを持つクライアントだけ。
- Gateway が事前チェック中に停止した Run は、まだ何も送っていない（`preparing`）ので次の起動で待機に戻り、コマンドからやり直す。

### Run の開始と復旧

開始時に設定と Memory を Run に固定し、外部処理の前に Attempt を `preparing` で保存する。プロンプト送信の直前に `sending`、
送信後に `running`、結果を受け取ったら生の結果を Attempt に保存してから確定処理をする。

| Gateway 再起動時の Attempt | 扱い |
|---|---|
| `preparing`（何も送っていない） | Run を待機に戻して実行する |
| 生の結果が保存済み | その結果から確定する（再実行しない） |
| `sending` / `running` | `needs_reconciliation`（成否不明）。自動で再送しない。ユーザーが確認して「確認済みにする」 |

子プロセス（sidecar / app-server）は Gateway の子なので、Gateway が落ちると一緒に終わる。生存プロセスへの再接続は行わない。
外部への副作用（コマンド、送信など）は DB トランザクションで一回限りにはできない。

## コンテキストと結果

各 Run は新しい会話として、指示・最新 Memory・最近の会話（最大 12 件 / 16KiB）・前回の Run・
（回答なら）回答と質問した Run のチェックポイント・起動理由から組み立てたプロンプトで実行する。通常セッションの履歴は読まない。
プロンプトは「目的（ユーザー）」「この実行」「権限」「既知の状態（データ）」「最近の会話」「新しい指示」「外部データ」「出力形式」に分け、
外部のページやファイルの文章を予定・権限を変える指示として扱わないこと、回答は送信・購入等の許可にならないことを明記する。

結果契約（JSON Schema は `resident.ResultSchema`。Claude の `outputFormat`、Codex の `outputSchema` に同じものを渡す）:

```json
{
  "schemaVersion": 1,
  "outcome": "changed",
  "summary": "確認対象のCIが復旧しました",
  "report": "詳細（Markdown）",
  "notification": {"requested": true, "key": "repo:pr:123:ci:run456:success"},
  "statePatch": [{"op": "set", "key": "lastCheckedRun", "value": "\"run456\""}],
  "memorySummary": null,
  "nextWakeAt": null,
  "question": null,
  "artifacts": [{"path": "reports/today.md", "title": "日報"}]
}
```

- strict な構造化出力に収めるため、Memory の値は `statePatch[].value` に JSON テキストとして入れる（`set` / `remove` のみ、
  キーは `[A-Za-z0-9_.:-]{1,64}`）。コードや SQL としては解釈しない。上限を超える patch は丸ごと拒否して Memory を保つ。
  要約は 8KiB で文字境界を保って切る。
- 正常なターン終了だけでは成功にしない。JSON でない・必須項目がない・未知の値は結果契約エラー（failed / contract）。
- `failed`・`blocked`・権限拒否があった `unchanged` は Memory を更新しない（前回の成功 Memory を保つ）。
- 実行中にユーザーが Memory を編集した場合、編集されたキーと要約は Run の結果で上書きしない（警告に記録）。
- 定期実行の `unchanged` は会話にも通知にも出さず、実行履歴だけに残す。手動・回答の Run は変化がなくても会話に返す。
- `nextWakeAt` は未来の一回限りの再確認予約: 最短 15 分後、最長 7 日、タスクの期限まで。タスクごとに 1 件で新しい予約が置き換え、
  定期予定の前後 15 分以内なら定期実行にまとめる。`null` を返すと保留中の自己予約は取り消す（失敗した Run は取り消さない）。
  一時停止・削除で取り消し、予定の編集では残す（期限外なら取り消し）。拒否・統合は Run の警告に残る。
- 成果物はタスクのフォルダ内のファイルだけを受け付け（symlink 解決後）、配信時にも同じ検証をする。

## 権限

アプリのフォームは指示・フォルダ・エージェント（モデル/effort）・予定・実行の上限・事前チェックを編集できる。
タスク名は省略すると指示の 1 行目になる。権限・通知は既定値（API と `hc` では指定もでき、アプリでの編集時も保持する）。
上限は 1 日の起動数（1〜288 回、既定 12 回）と 1 回の制限時間（1〜240 分、既定 30 分）を設定する。
手動実行・回答は起動数に含まれるが、回数上限に達しても実行できる。API で設定したタスクの期限は編集時にも保持する。

無人実行の範囲は権限プロファイルで決め、各エージェントの権限機構で強制する（プロンプトの文言には頼らない）。既定は `workspace_write`。

| | フォルダ内の編集可（`workspace_write`、既定） | 読み取りのみ（`read_only`） |
|---|---|---|
| Claude | auto モード（モデルが対応していれば。Claude Code の分類器が各操作を判断し、人に聞く操作は拒否）、未対応なら `acceptEdits` | permissionMode `default`、Read/Glob/Grep/LS を許可、Edit/Write/MultiEdit/NotebookEdit を禁止 |
| DSH | preset `workspace-write`、追加の承認要求は拒否 | preset `read-only`、追加の承認要求は拒否 |
| Codex | sandbox `workspace-write`、approvalPolicy `on-request` を auto review（`approvalsReviewer: auto_review`）が人の代わりに判断 | sandbox `read-only`、approvalPolicy `never` |

- ネットワークは常に使える（Claude は WebSearch/WebFetch を許可、Codex は sandbox の `networkAccess: true`、DSH は標準プロファイルのネットワーク設定）。
  古いクライアントが送る `permissions.network` は無視する。
- Claude には追加の許可ルール（例 `Bash(gh pr view:*)`、`mcp__github__get_issue`）を指定できる。無制限の `Bash` は不可。
  ユーザー・プロジェクトの設定にある許可ルールは `claude -p` と同様に有効（Claude Code が既定で許す読み取り専用コマンドも含む）。
- それ以外の権限要求は自動で拒否する（Claude: 常駐用の `canUseTool` が拒否して記録、Codex: auto review の却下を記録し、Gateway に来た承認要求には decline、DSH: SDK の承認要求に rejected）。
  通常セッションの承認設定は変えない。bypassPermissions は使わない。
- 必要な権限・ログイン・判断がなければ、エージェントは `blocked` と質問を返し、Run は `waiting_user` になる（プロセスは待たせない）。
  回答は期限（7 日）と対象 Run を照合して保存し、質問した Run のチェックポイントから新しい Run を始める。古い承認 ID は再送しない。
- フォルダは workspaceRoots の中だけ。エージェント自身が信頼していないフォルダは、作成時に明示の信頼確認（`trust: true`）が必要
  （プロジェクトの hooks / MCP が無人で動くため）。確認はタスクに記録し、Gateway はエージェントの信頼設定に書き込まない。
  ただし Codex は `workspace-write` の実行で、そのフォルダを `~/.codex/config.toml` に `trust_level = "trusted"` として自分で追加する
  （codex-cli 0.166.0 で確認）。
- Claude のログイン・API キー課金のガードは通常セッションと同じ（`claudeAllowApiKey` がなければ API キーでは実行しない）。
- Claudeサブスクは Claude Code、ChatGPTサブスクは Codex を選び、Mac上の各CLIでログインする。常駐もその認証を使う。
  DSH自体の別モデル接続は `dshModels` で設定する。DSHのChatGPTログインは未実装で、Anthropicサブスクは公式Claude Code経由を使う（[接続と制限](deepseek-harness.md#model-routes-and-subscriptions)）。
- DSH は最終回答に結果スキーマを指示し、Gateway で検証する。SDKに出力スキーマ制約はないため、不正な結果は契約エラーになり、自動で再送しない。
- モデル・effort は保存時に Provider のカタログで検証し、実行時に提供されないモデルは置き換えずに失敗させる。
- メール・Web 検索などの外部連携は、この Mac で設定済みの CLI / MCP だけが使える。未設定ならエージェントが理由を返す。

## 通知

結果・Memory 更新・Outbox 作成を 1 トランザクションで確定する。モデルの通知希望に加えて Gateway 側で判断する:

| 種類 | 条件 |
|---|---|
| 変化（change） | `outcome: changed` かつ、通知モード `changes` なら `notification.requested`（`always` なら常に、`never` なら出さない）。通知済みキーは再通知しない。クールダウン中は抑制 |
| 判断待ち（question） | 質問を返したとき（`onQuestion`） |
| 失敗（failure） | failed / timed_out / 結果契約エラー / blocked / 成否不明（`onFailure`）。次の成功までは最初の 1 回だけ |

静穏時間（例 23:00–07:00、タスクの timezone）中の通知は終わるまで保留する。判断は Run の `notifyDecision` に残る。

配送は Outbox から行い、FCM の data message `{type: "resident", taskId, runId, notificationId, kind, title, body, gatewayId}` を
送る（`sessionId` は含まないので旧アプリは無視する）。端末ごとに届いた記録を持ち、失敗した端末だけに最大 6 回まで再送する。
送信後・記録前に停止すると同じ通知が再送されることがあるが、Android は同じ `notificationId` を二度表示せず、
タスクごとに 1 件の通知を置き換える。外部配送の exactly-once は保証しない。アプリ内の報告が正本。

- Android: 「常駐」チャンネルグループの「報告」「判断待ち」「失敗」（Sessions とは別のグループで、シェードでも別の束になる）。タップで常駐タブの該当タスクを開く。
- Mac UI: `GET /v1/resident/notifications` を 15 秒ごとに読み、新しいものをシステム通知とアプリ内バナー（「開く」で該当タスク）で出す。
- 未読数は報告メッセージだけを数える（変化のない定期実行は数えない）。報告タブを開くと既読になる。

## セッションへの昇格

「毎時調べて、変化があったらその場で相談する」ための仕組み。Run は調査したときの会話をそのまま残し、
ユーザーが選んだ Run を通常のセッションにできる。

- Run の会話は通常セッションと同じく Claude Code / Codex の履歴として残り、Gateway は消さない（変化のない定期実行も、
  タスクを削除しても残る）。プロンプトを送る直前に Run の `conversation` を `kept` にする。
- `POST /v1/resident/runs/{id}/promote` が昇格する: 終わった Run で `kept` のときだけ。題名「タスク名（常駐 MM/DD HH:MM）」を付け
  （Claude は transcript に `custom-title`、Codex は `thread/name/set`）、`promoted` にしてセッション一覧に出し、
  通常セッションとして再開する（`Launcher.Resume`）。以後は普通のセッションで、承認はアプリに来る。2 回目以降は同じセッションを返す
  （止まっていれば再開する）。
- 昇格したセッションの先頭は常駐のプロンプト、最後の応答は結果契約の JSON（Claude は StructuredOutput ツール）になる。
  続けて話しかけると通常の文章で答える。
- 残した会話は Gateway のセッション一覧には昇格するまで出ないが、Claude Code の `claude --resume` 一覧や
  `~/.codex/sessions` には通常のセッションと同じように現れる。

## 画面

- 常駐ホーム: タスク名、状態（稼働中・待機中・実行中・判断待ち・要確認・一時停止）、次回予定、最終成功、最新の重要報告、判断待ち、未読数。
  空状態・取得エラー（前回の内容を残す）・通信断・未対応 Gateway（`/v1/capabilities` に `resident` がない）を区別する。
- タスク詳細: 状態・次回予定・今日の起動数を表示し、「実行情報」を開くと次の 3 回・自己予約・最終成功を表示する。
  今すぐ実行／今回の実行を停止／一時停止（期間を選ぶ: 3 時間・明日の朝・1 週間・再開するまで）・再開、判断待ちへの回答、
  タブ「報告」（新しい順に上から表示し、更新でも読んでいる位置を保つ。「最新の報告へ」で先頭に戻る）・「実行履歴」（停止、成否不明の確認、詳細・使用量・警告・成果物）・「記憶」（要約と状態の編集）・「設定」。
  会話を残している Run の報告と実行履歴には「セッションで続ける」（昇格済みなら「セッションを開く」）があり、
  Android は Sessions タブ、Mac UI はフォーカス中のペインでそのセッションを開く。
- 作成・編集: 名前、指示、フォルダ（選択ダイアログ）、エージェント・モデル・effort、予定（一回限り/毎日/毎週/cron/手動）と timezone、
  保存前に次の 3 回を表示、実行の上限、事前チェックのコマンド（空にすると外す。時間制限は API / `hc` で指定）。上限の範囲外や事前チェックより短い制限時間は保存前に表示し、その他の Gateway の拒否理由も表示する。
  予定・上限はフォーム、権限・通知条件・期限は API / `hc` で変更する（会話の自然文では変わらない）。
- Android は Sessions と常駐が別の back stack を持つ（Navigation 3）。Mac UI はヘッダーで切り替え、どちらの状態も保つ。
- 接続先を変えると、別接続のタスクや状態は捨てる（キャッシュは接続ごと、Room には保存しない）。

## 設定（`config.json`）

| キー | 既定 | |
|---|---|---|
| `residentMaxConcurrentRuns` | 1 | 全タスク合計の同時実行数 |
| `residentCatchUpHours` | 24 | 逃した予定を補完する範囲 |

タスクごとの上限（1 Run の分数、1 日の起動数、期限）、通知条件はタスクの設定。

## 検証

- `go test ./internal/resident/...`: 予定（timezone・夏時間）、結果契約、Fake Runner での実行・通知・冪等・並行・復旧・自己予約（Fake Clock）。
- `go test ./internal/providers/...`: 常駐 Runner の Provider 境界（偽 sidecar / 偽 app-server、プロセスツリーの終了）。
- 実エージェントでの確認は、独立した config/state・scratch フォルダ・未使用ポートの使い捨て Gateway で行う（[AGENTS.md](../AGENTS.md)）。
  Mac UI の操作テストは `HERDR_RESIDENT_E2E_URL` / `_TOKEN` / `_CWD`（任意で `_SHOTS`）を指定したときだけ動く:
  `./gradlew :desktopApp:test --tests '*DesktopResidentFlowTest*' --rerun`。
- Android の端末テスト（[android-device-testing.md](android-device-testing.md)）に常駐の合成 push → 通知 → タップ遷移 → 同一 ID の再表示なしを含む。
