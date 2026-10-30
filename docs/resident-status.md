# 常駐機能 実装状況（作業ログ）

実装の引き継ぎ用。完了したら [resident.md](resident.md) とコードが正となる。

## 作業項目

- [ ] Gateway: `internal/resident` ストア（SQLite, スキーマ版管理）
- [ ] Gateway: 予定計算（once/daily/weekly/cron, IANA tz, DST）
- [ ] Gateway: 結果契約（JSON Schema + 業務検証 + statePatch）
- [ ] Gateway: スケジューラ（補完・スキップ・自己予約・日次上限）
- [ ] Gateway: 実行キュー（タスク毎1・全体既定1・タイムアウト・停止）
- [ ] Gateway: 復旧（再起動時 needs_reconciliation / 未送信は再キュー / 保存済み結果の取り込み）
- [ ] Gateway: 通知 Outbox（条件・重複キー・クールダウン・静穏時間・再試行）
- [ ] Gateway: API `/v1/resident/*`, `/v1/capabilities`
- [ ] Claude: sidecar 常駐実行経路（persistSession:false, outputFormat, 権限）
- [ ] Codex: 常駐用 app-server（ephemeral, outputSchema, sandbox）
- [ ] 通常セッションへの非混入の確認（一覧・Watcher・hc ls）
- [ ] hc: `hc resident ...`
- [ ] shared: DTO / GatewayApi / 常駐画面
- [ ] Android: トップレベルタブ・通知遷移
- [ ] Desktop: タブ切替・通知
- [ ] ドキュメント（architecture / api / cli / README / resident.md）
- [ ] 実 Provider 確認（独立 config/state）
- [ ] エミュレーター・Desktop 画面確認、release APK・Desktop ビルド

## 未解決事項

（随時追記）
