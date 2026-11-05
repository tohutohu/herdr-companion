# PLaMo 3 Translate のローカル翻訳

Apple silicon Mac の Gateway メニューバー →「ローカル翻訳 · PLaMo 3 Translate」で利用する。
Android や Compose Desktop に MLX を導入する必要はない。

1. Gateway を起動する。
2. 0.8B（重み約1 GB）・2B（約2.4 GB）・8B（約6.1 GB）を選んで「ダウンロードして起動」。実行時には重み以外のメモリも使う。
3. 必要なモードを個別にオンにする。
   - プロンプト：日本語を含む通常の送信文・新規セッションの初回文を英訳してエージェントへ送る。英語だけの文とスラッシュコマンドはそのまま。
   - 表示：セッションタイトル・報告本文・承認画面のタイトル、質問、選択肢の表示文を日本語にする。原文表示へ切替可能（タイトル原文はMac UIのセッション詳細に表示）。
4. 「翻訳を停止」でメモリを解放する。起動したまま Gateway を終了した場合は次回も自動起動する。モード設定は保持する。

Android・Mac UI の「New session」には「Translation mode」スイッチがあり、セッションごとにオン／オフを選べる（初期値はオフ）。オンではプロンプトと表示の両方を翻訳し、オフでは両方を無効にする。初回文・以後の送信・タイトル・報告・承認表示に適用され、フォルダの信頼確認、Gateway の再起動、セッションの再開後も選択を保持する。翻訳モデルは事前に Gateway メニューから起動する。既存セッションや指定のない旧クライアントの起動はメニューのモード設定に従う。

自動起動が有効で翻訳プロセスが異常終了した場合、次の翻訳要求でモデルを起動し直す。起動途中の要求は準備完了を待つ。手動で「翻訳を停止」した場合は自動復旧しない。モデルが停止中でも保存済みの翻訳は再利用できる。新しく翻訳が必要なのにモデルが停止・失敗している場合、日本語の送信は未送信のエラーにする。翻訳を再起動するかモードをオフにして再試行できる。翻訳は送信 receipt の claim より前に行い、翻訳失敗の再試行と送信成功後の重複防止を両立する。

端末の通信切断や要求のキャンセルでは進行中の推論を中断せず、完了した訳をキャッシュする。推論処理は最大2分で終了する。同じ文への再要求は進行中の処理を待ち、成功した訳を再利用する。

報告の翻訳はターン完了後、表示中の会話の新しい128テキストブロックまでをバックグラウンドで行う。翻訳待ち・失敗中は原文を表示する。承認画面は報告の翻訳より優先する。推論内容、ツール呼び出し・ツール結果は翻訳しない。コードフェンス、インラインコード、URL、選択肢のプレビューは保持する。JSON の本文も変更しない。

承認の ID・decision・質問 ID・実際に返す選択肢の値は元のまま。翻訳された表示は `interaction.translation` に別添し、元の interaction は変更しない。承認対象のコマンド・パスを含む detail は原文を残し、プロバイダが別途持つ説明文だけを訳して追記する。日本語表示と原文を切り替えても選択状態は維持する。

通常セッションのメッセージ API に適用する。常駐タスクの自動実行、`btw`、通知本文の翻訳は対象外。モデル自体の訳の品質はサイズと文章に依存する。

## 実装と保存先

Gateway の `internal/translation` がモデルを保持する Python プロセスを所有し、標準入出力の JSON Lines で通信する。翻訳用 HTTP サーバーや追加の公開ポートは作らない。Gateway 終了・停止・タイムアウトで所有するプロセス群を終了する。

初回に SHA-256 固定の uv 0.13.7、管理された Python 3.12、バージョン固定の MLX / mlx-lm 等を導入する。PFN 公式の MLX 4bit モデルを固定 revision から取得する。モデルに同梱されたカスタムコードを使用する。推論プロセスは `HF_HUB_OFFLINE=1`、導入時のみダウンロードを許可する。

保存先は `$HERDR_MOBILE_STATE_DIR/translation/`（メニューバー版の既定は `~/.local/state/herdr-mobile/desktop/translation/`）。モデルと実行環境はアプリ更新後も再利用する。

- `settings.json`：選択モデル・モード・次回の自動起動。
- `models/`・`venv/`・`python/`・`uv-cache/`：実行環境とモデル。
- `setup.log`・`worker.log`：導入・起動の診断。翻訳リクエスト本文はログに出さない。
- `cache.json`：送信文・報告・タイトル・承認表示の成功した訳。private、0600。最大256件・訳文合計8 MiBで、最近使ったものを残す。モデルサイズ・固定revision・翻訳方向・翻訳workerの内容・原文のハッシュで識別するため、モデルや翻訳処理を変更すると別のキャッシュになる。失敗は保存しない。削除しても再翻訳できる。
- `timings.jsonl`・`timings.1.jsonl`：private、0600。各5 MiB、最大2世代。モデルサイズ・revision・翻訳方向・入出力のバイト数・結果・所要時間（ms）を記録し、原文・訳文・エラー本文は記録しない。`request` は要求全体の `elapsedMs`、推論待ちの `queueMs`、`cacheHit` を持つ。`inference` は端末が切断しても最後まで記録し、準備待ちの `startupMs` とモデル処理の `inferenceMs` を分ける。`worker_start` は導入を含む起動時間、`worker_exit` は稼働時間と終了状態（`exit`）を残す。表示のポーリングで既存の訳を返すたびには記録しない。モデルサイズ別の速度比較には成功した `inference` を使い、文の長さと方向もそろえて比較する。
- `prompts/`：送信前の日本語と英訳との対応（private、0600）。エージェント側の履歴は英語のまま、アプリでは入力した日本語と実際に送った英語を確認できる。原文データでありキャッシュとして削除しない。

翻訳キャッシュはGatewayの再起動、モデルの停止・切替後も保持する。プロバイダの履歴とキャッシュは変更しない。表示モードを一度有効にした Gateway は、古いブロックの翻訳完了・表示解除も端末に届くようメッセージを全件スナップショットで返す。ETag が同じ場合は従来どおり304を返す。旧クライアントは報告の日本語本文を表示できるが、原文切替と承認画面の訳には更新版のクライアントが必要。

## 検証

```sh
cd gateway
env -u GOROOT go test ./...
env -u GOROOT go test -race ./internal/translation ./internal/api
env -u GOROOT go vet ./...
python3 -m unittest discover -s internal/translation -p 'test_worker.py'
# 実モデルをダウンロードする任意のローカル試験（隔離した保存先を指定）
HERDR_PLAMO_SMOKE_DIR=/tmp/herdr-plamo-smoke go test ./internal/translation -run Testローカル実モデル翻訳 -v -timeout 15m
# 既定は0.8B。サイズを指定する場合は HERDR_PLAMO_SMOKE_MODEL=8b を追加
```

配布元・プロンプト形式・モデルの利用条件は [PFN 公式モデルカード](https://huggingface.co/pfnet/plamo-3-translate-0.8b-MLX-4bit) と [PLaMo Community License](https://plamo.preferredai.jp/info/plamo-community-license-ja) を参照。
