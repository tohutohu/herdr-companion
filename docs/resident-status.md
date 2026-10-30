# 常駐機能 実装状況（作業ログ）

実装の引き継ぎ用。完了したら [resident.md](resident.md) とコードが正となる。

## 作業項目

- [x] Gateway: `internal/resident` ストア（SQLite, スキーマ版管理）
- [x] Gateway: 予定計算（once/daily/weekly/cron, IANA tz, DST）
- [x] Gateway: 結果契約（JSON Schema + 業務検証 + statePatch）
- [x] Gateway: スケジューラ（補完・スキップ・自己予約・日次上限）
- [x] Gateway: 実行キュー（タスク毎1・全体既定1・タイムアウト・停止）
- [x] Gateway: 復旧（再起動時 needs_reconciliation / 未送信は再キュー / 保存済み結果の取り込み）
- [x] Gateway: 通知 Outbox（条件・重複キー・クールダウン・静穏時間・再試行）
- [x] Gateway: API `/v1/resident/*`, `/v1/capabilities`
- [x] Claude: sidecar 常駐実行経路（persistSession:false, outputFormat, 権限）
- [x] Codex: 常駐用 app-server（ephemeral, outputSchema, sandbox）
- [x] 通常セッションへの非混入の確認（一覧・Watcher・hc ls）
- [x] hc: `hc resident ...`
- [ ] shared: DTO / GatewayApi / 常駐画面
- [ ] Android: トップレベルタブ・通知遷移
- [ ] Desktop: タブ切替・通知
- [ ] ドキュメント（architecture / api / cli / README / resident.md）
- [ ] 実 Provider 確認（独立 config/state）
- [ ] エミュレーター・Desktop 画面確認、release APK・Desktop ビルド

## 実 Provider 確認（2026-10-30, 使い捨て Gateway 127.0.0.1:18793, /tmp/resident-e2e）

- Claude haiku/low・Codex gpt-6-luna/low の両方で有効な結果契約（changed, statePatch, 通知キー）
- 追加指示が Memory（state.fruit）を参照して返答
- read_only で `touch` を Bash で試行 → 拒否され blocked + 質問で waiting_user、ファイル未作成
- 実行停止：Codex/Claude とも `sleep` の子プロセスまで終了、通常の app-server は残る
- Gateway を実行中に SIGTERM → 再起動後 needs_reconciliation、再送なし、子プロセスなし
- Claude transcript なし（~/.claude/projects に空の memory/ ディレクトリのみ）、Codex rollout なし、/v1/sessions に出ない

## 未解決事項

- Codex の turn/interrupt + thread/unsubscribe ではコマンドが止まらない → 実行ごとに app-server/sidecar を起動しプロセスツリーごと終了する方式に変更済み
