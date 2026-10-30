# 常駐機能 実装状況（作業ログ）

仕様は [resident.md](resident.md)、API は [api.md](api.md#常駐タスク)、CLI は [cli.md](cli.md)。ここは実装と検証の記録。

## 作業項目

- [x] Gateway: `internal/resident`（SQLite・スキーマ版管理・予定・結果契約・スケジューラ・実行キュー・復旧・通知 Outbox）
- [x] Gateway: API `/v1/resident/*`, `/v1/capabilities`
- [x] Claude: sidecar の常駐モード（persistSession:false, outputFormat, 権限）／Run ごとの sidecar
- [x] Codex: Run ごとの app-server（ephemeral, outputSchema, sandbox, 編集可は auto review・読み取りのみは approvals never）
- [x] Run 終了・停止・時間切れでプロセスツリーごと終了
- [x] 通常セッションへの非混入（一覧の除外、Watcher・Manager 非登録）
- [x] hc: `hc resident ...`
- [x] shared: DTO / GatewayApi / 常駐画面（一覧・詳細・作成編集）
- [x] Android: トップレベルタブ（Navigation 3 の 2 本の back stack）・通知・通知からの遷移
- [x] Desktop: ヘッダー切替・常駐ビュー・Outbox を読む通知とアプリ内バナー
- [x] ドキュメント（resident / architecture / api / cli / README / AGENTS）

## 検証（2026-10-30）

- Gateway: `go vet ./...`、`go test ./...`（resident は `-race -count=10` でも確認）
- sidecar: `bun run typecheck`、`bun test`（4 件）、`bun run build`
- Android/共通/Desktop: `assembleDebug testDebugUnitTest`、`:shared:allTests :gatewayClient:test :desktopApp:test :app:lintDebug :app:lintRelease`、
  `assembleRelease`（R8・リソース縮小）、`:desktopApp:createReleaseDistributable`
- 端末テスト（エミュレーター HERDR_RESIDENT_API37）: 8 件 OK（既存のセッション通知タップ・通知返信・再試行 + 常駐の合成 push → 通知 → タップ遷移 → 同一 ID 再表示なし）
- 使い捨て Gateway（独立 config/state、/tmp の scratch リポジトリ、127.0.0.1:18793）で実エージェント:
  - Claude haiku/low・Codex gpt-6-luna/low とも有効な結果契約、指定モデル・effort で実行
  - 追加指示が Memory を参照、read_only で書き込みコマンドを拒否 → blocked + 質問 → 回答 → チェックポイントから再実行（ファイル未作成）
  - 停止（Claude/Codex とも子プロセスなし）、実行中の Gateway 停止 → 再起動で needs_reconciliation・再送なし
  - Claude transcript なし、Codex rollout・信頼設定の追加なし、`/v1/sessions` に出ない
  - 通常セッション（開始・effort 変更・アーカイブ・再開・送信）が正常、常駐は一覧に混ざらない
- Android エミュレーター画面: 常駐ホーム、作成フォーム（次の 3 回、無効な cron の理由表示、信頼確認）、今すぐ実行 → 実行中 → 報告、追加指示、停止、実行履歴、通知 Intent からの遷移、判断待ちへの回答。release APK でも表示を確認
- Mac UI: `DesktopResidentFlowTest`（実 Gateway・Codex）で作成 → 実行 → 報告 → 追加指示 → 停止。リリース jar（ProGuard 後）のプローブで常駐 DTO の取得を確認

## 未実施・制約

- 実 FCM 配送: 使い捨て Gateway に Firebase 認証情報を置かず、本番設定も使わないため未実施（SDK への合成 push で代替）
- Mac UI のリリース版ウィンドウの起動確認: 本物の Mac UI が起動中で Preferences を共有するため行わず、上記の UI テストとプローブで代替
- 実機（Pixel 7a）: 未接続のため未実施
- 実際の Mac のスリープ・夏時間: Fake Clock のテストのみ
- Android の常駐データは接続ごとのメモリキャッシュで、Room には保存しない（アプリ再起動後の通信断では前回の一覧を出さず理由を表示）
- Mac UI のシステム通知はクリックで遷移できない（osascript）。アプリ内バナーの「開く」で該当タスクへ遷移
- 結果契約エラーや設定の拒否理由など一部の Gateway メッセージは英語
- 本番アプリの置き換え、稼働中 Gateway の再起動、push、リリース、実際の定期タスクの有効化は行っていない
