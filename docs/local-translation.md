# PLaMo 3 Translate のローカル翻訳

Apple silicon Mac の Gateway メニューバー →「ローカル翻訳 · PLaMo 3 Translate」で利用する。
Android や Compose Desktop に MLX を導入する必要はない。

1. Gateway を起動する。
2. 0.8B（重み約1 GB）・2B（約2.4 GB）・8B（約6.1 GB）を選んで「ダウンロードして起動」。実行時には重み以外のメモリも使う。
3. 必要なモードを個別にオンにする。
   - プロンプト：日本語を含む通常の送信文・新規セッションの初回文を英訳してエージェントへ送る。英語だけの文とスラッシュコマンドはそのまま。
   - 表示：セッションタイトル・報告本文・承認画面のタイトル、質問、選択肢の表示文を日本語にする。原文表示へ切替可能（タイトル原文はMac UIのセッション詳細に表示）。
4. 「翻訳を停止」でメモリを解放する。起動したまま Gateway を終了した場合は次回も自動起動する。モード設定は保持する。

プロンプト翻訳が有効なのにモデルが停止・失敗している場合、日本語の送信は未送信のエラーにする。翻訳を再起動するかモードをオフにして再試行できる。翻訳は送信 receipt の claim より前に行い、翻訳失敗の再試行と送信成功後の重複防止を両立する。

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
- `prompts/`：送信前の日本語と英訳との対応（private、0600）。エージェント側の履歴は英語のまま、アプリでは入力した日本語と実際に送った英語を確認できる。原文データでありキャッシュとして削除しない。

回答の訳はメモリ内だけに保持（最大256件・8 MiB）。プロバイダの履歴とキャッシュは変更しない。表示モードを一度有効にした Gateway は、古いブロックの翻訳完了・表示解除も端末に届くようメッセージを全件スナップショットで返す。ETag が同じ場合は従来どおり304を返す。旧クライアントは報告の日本語本文を表示できるが、原文切替と承認画面の訳には更新版のクライアントが必要。

## 検証

```sh
cd gateway
env -u GOROOT go test ./...
env -u GOROOT go test -race ./internal/translation ./internal/api
env -u GOROOT go vet ./...
python3 -m unittest discover -s internal/translation -p 'test_worker.py'
# 実モデルをダウンロードする任意のローカル試験（隔離した保存先を指定）
HERDR_PLAMO_SMOKE_DIR=/tmp/herdr-plamo-smoke go test ./internal/translation -run Testローカル実モデル翻訳 -v -timeout 15m
```

配布元・プロンプト形式・モデルの利用条件は [PFN 公式モデルカード](https://huggingface.co/pfnet/plamo-3-translate-0.8b-MLX-4bit) と [PLaMo Community License](https://plamo.preferredai.jp/info/plamo-community-license-ja) を参照。
