# Androidプロジェクト品質監査

監査日: 2026-10-15。現在の追加対応と検証は末尾の「9. 追補」を優先する。基準コミット: `1104f86`（1.6.1）。Androidに加え、その共通UI・HTTPクライアント・Desktop利用側と、Gateway/Managerの信頼境界を調査した。
コード追跡、静的解析、独立した3担当の調査、既存テスト、障害条件の回帰テストを統合した記録である。全ファイルの行単位レビュー、侵入試験、全端末の動作保証ではない。

## 1. 構成と重要なデータフロー

### 目的と制約

MacでHerdr内のClaude Code / Codex / OpenCode / Devinを動かし、Androidから進捗確認、通知受信、メッセージ送信、質問・承認への回答、新規セッション開始、添付・ファイル確認を行う。
会話の正本は各providerとHerdr。Gatewayは投影と変換を担当し、AndroidのRoomは再取得可能な表示キャッシュである。
HTTPを一律禁止するとTailscale内通信という既存用途を壊す。GatewayやHerdrの再起動、実ユーザーのpaneへの送信は監査で実行していない。

### モジュールと依存方向

| 対象 | 責務・依存関係 |
|---|---|
| `android/app` | AndroidのActivity、Navigation 3、ViewModel、Room、DataStore、FCM、WorkManager、文書URI/MediaStore。`shared`と`gatewayClient`へ依存 |
| `android/shared` | KMPのcommonMainにDTO・表示モデル・純粋ロジック・Compose UI。Android/JVM Desktop target。Android ContextやRoomを持ち込まない |
| `android/gatewayClient` | Android/Desktop共用のJVM OkHttpクライアント。`shared`のDTO/Settingsに依存 |
| `android/desktopApp` | Compose Desktop、独自状態・設定・ウィンドウ・通知・更新。`shared`/`gatewayClient`を利用。Android Roomとは別のインメモリ読取系 |
| `gateway` | Go HTTP認証/API、Herdr socket、4 provider、ファイル/upload、起動/trust/worktree、usage/price、FCM watcher。任意のJev判定は既存仕様として境界確認のみ |
| `macos` | SwiftUIメニューバーGateway Manager、子プロセス管理、設定/pairing、DMG/更新。Compose Desktopアプリとは別物 |
| `.github` / `scripts` | バージョン共通化、Android APK・Mac DMG・Linux deb・CLI配布。現在のCIはタグと手動起動 |

技術構成はKotlin 2.4.20、AGP 9.3.1、Compose BOM 2026.06.01 / Multiplatform 1.12.1、Navigation 3、Coroutines/Flow、OkHttp 5.5、Room 2.8.4、DataStore、WorkManager、Media3、Coil、FCM。minSdk 29 / targetSdk 36 / compileSdk 37、JDK 17、Gradle 9.6。
`compileSdk 37`はCoil依存に対応する既存条件。依存関係の一括更新は行っていない。

### 主要フローの追跡

1. **設定・pairing**: Settings画面/QR → PairingのURL・応答検証 → `AppContainer.configure` → DataStore。接続変更は次プロセスへstageし、cache resetとFirebase初期化を先に完了。`connectionId`で古い通知/Workerを拒否する。
2. **一覧**: STARTED中のRoute更新 → `SessionRepository.refreshSessions` → Gateway `/v1/sessions` → provider/Herdr → Room `replaceListing` → Flow → UiModel → 共通`SessionListScreen`。一覧の正本をUI内へ複製しない。
3. **詳細**: Navigation keyのsession ID → ViewModel → Room Flow → 共通`SessionDetailScreen`。poll/FCM prefetch/送信後refreshはGatewayのanchorを含む増分を`replaceFrom` transactionで保存する。今回、ETagの確定を保存後へ変更した。
4. **送信・回答**: Composer → Android Outboxのアプリscopeで添付uploadと送信 → Gateway → provider。質問/承認はinteraction IDと回答DTOを送る。Gatewayのprovider別変換が端末キー操作/daemon APIを選択する。Outbox/SessionStartsは画面離脱には耐えるが、プロセス終了に耐える永続queueではない。
5. **通知**: Gateway watcher → data-only FCM → `PushService`が通知表示し`PrefetchWorker`をenqueue → Roomへ取得。タップは接続IDを確認して詳細へ。RemoteInput返信は非公開Receiver → `ReplyWorker` → POST。WorkManagerはネットワーク条件/backoff/試行上限を持つ。
6. **新規起動・復帰**: フォルダ/モデル/effort/プロンプト → 任意のdirectory check → アプリscopeの`SessionStarts` → Gateway launcher。trust要求、部分起動、Terminal/Continueに対応し、確認失敗時に安易な再起動を促さない。
7. **添付・閲覧**: OS文書picker → IOでURIメタデータ → 上限付き読み込み/upload。ファイルstatでプレビュー/Media3/ダウンロードを選択、保存はMediaStoreのpending行→成功後公開。画像認証はGatewayとscheme/host/portが同一の宛先に限定。
8. **Desktop**: 同じUI/DTO/API → `DesktopAppState`/Repository → pane・独立ウィンドウ。listMutex、可視セッションのdetail poll、layout/preferences保存。ETag receiptも表示反映後にackするよう修正した。

Room v8は1→8のAutoMigrationと各schemaを保持し、メッセージ末尾置換・一覧置換はそれぞれtransaction。破壊的fallbackは会話の原本ではなくcacheに限られるが、migration実DBテストがない。SessionEntityとMessageEntity間の書込み全体は単一transactionではない。
時刻はDTOのISO timestampを`Instant`のepoch millisへ変換する。未知値は0となる実装で、表示順への影響はあり得るが、今回の入力経路調査で具体的なtimezone障害は確認しなかった。

## 2. 調査カバレッジ

| 範囲 | 確認した機能・方法 | 検証の限界 |
|---|---|---|
| `app` | Manifest/debug Manifest、DI container、全データ責務、Room/Settings、Outbox/Starts、通知・返信、一覧/詳細/アーカイブ/新規起動/起動待ち/設定/usage/Terminal/画像/ファイル/Media、全既存単体テストとLint | Android instrumentedテストは未導入。実サービス宛の送信・FCM配信は実行せず |
| `shared` | DTO/Mapper、一覧/検索/選択/スワイプ、会話/質問/添付、Markdown/ハイライト、テーマ/Insets/アニメーション、新規フォーム/並べ替え、common/desktop tests | Desktop上のCompose検証でありAndroid OS固有のBundle/IME/戻るジェスチャーまでは保証しない |
| `gatewayClient` | 認証/URL、全HTTP系統、ETag、長時間POST、ファイル/画像download、APIのMockWebServerテスト | `:gatewayClient:test`はNO-SOURCE。現在APIテストはappのtestに存在 |
| `desktopApp` | 接続源・手動設定、AppState/repository、複数pane/切離し、検索/設定保存、画像・添付、通知/Terminal/更新、全JVMテスト | Mac UIの再配布・再起動、Linux実行/パッケージは行わず |
| `gateway` | architecture/HTTP契約、認証/pairing/ログ、provider呼出境界、通知・送信・cacheの関係、全Go test/vet | provider各実装の全行精査・実CLI起動・Jevの外部評価は対象外 |
| `macos` / 配布 | ManagerとDesktopの区別、署名/更新・release workflow、Swiftテスト、R8設定/解析 | 公開済みAPKの署名比較・Play Console評価・DMG再生成は未実施 |

Google公式skillsは`android-intent-security`、`edge-to-edge`、`adaptive`、`testing-setup`、`r8-analyzer`を観点別に利用した。手動DIは既存の責務とテストに足りており、DI製品やUseCase層の追加は選ばなかった。
開始時の未追跡`.agents/skills/adaptive/`・`.claude/skills/adaptive`は保持し、commitしていない。

## 3. 強みと主要リスク

### 維持すべき設計

- RoomをAndroid UIの読取元に限定し、共通Composableは状態とactionを受ける。プラットフォーム固有I/OがKMP UIへ漏れていない。
- Navigation 3のserializable back stack・ViewModel scope・遷移中二重操作抑制、画面単位のrefresh制御がある。
- `enableEdgeToEdge`、`adjustResize`、入力バーのIME inset、画像のcutout配慮、Material 3のcontainer/content色ペアがある。
- foregroundのライフサイクルで一覧/詳細/Terminal pollを制限。LazyColumnの安定key、Media3の停止時pause/dispose releaseがある。
- ReplyReceiver/PushServiceは非exported。debug ReceiverはDUMP保護。通知遷移はimmutable PendingIntent、RemoteInputだけ明示的宛先のmutable PendingIntent。
- `allowBackup=false`、共有APKへのprivate key非埋込み、pairingのredirect禁止/応答上限/一回限りコード、Gatewayのconstant-time認証とqueryを出さないアクセスログがある。
- releaseのR8とresource shrinkingが両方有効。独自keepはserializationの説明コメントだけで、広いpackage keepの追加は不要。

P0と断定できる問題は今回確認していない。中心的なリスクは、通知からの返信/更新の障害条件、画面寿命と入力保持、複数refreshの競合、配布時の署名継続性である。

## 4. 優先度順の改善バックログ

「再現済」は自動テストで旧動作が失敗することを確認したもの。「コード確認」は経路・制約を確認したもの。「要計測/要端末確認」は発生頻度や体感影響をまだ測っていないもの。
工数は実装と必要な回帰検証を含む概算: 小=半日程度、中=1〜3日程度。実サービス条件により変動する。

| ID / 優先度 / 状態 | 根拠・原因と影響 | 対応・代替・リスク/効果・検証 |
|---|---|---|
| A01 / P1 / **修正済・再現済** | `gatewayClient/.../GatewayApi.kt:146`、`app/.../data/SessionRepository.kt:67`。受信直後のETag保存→キャンセル/DB保存失敗→次回304で未表示更新をスキップ。質問・承認の表示信頼性に影響 | 消費側の反映後ack。小・低リスク・Android/Desktop両方に効果。受信途中cancel/未ack/ack後304/anchor変更をテスト。本文cacheで毎回再適用する代替はメモリ/DB負荷が増えるため不採用 |
| A02 / P1 / **修正済・境界検証済** | `app/.../data/Attachment.kt:30`、新`UploadBytes.kt:10`。旧版は全量read後に20MiBを判定。巨大文書でプロセスOOMの可能性 | 上限+1 byteで停止。小・低リスク。実OOMを故意発生させず消費バイト数を検証。サイズqueryだけでは未知サイズ/不正申告を防げず、streaming multipart化は今回不要 |
| A03 / P1 / **修正済・再現済** | `shared/.../ui/detail/InteractionCard.kt:254`。質問選択と自由入力がrememberのみで再生成/行破棄により消失 | 最小の選択label/文字列をSaverで保存。小・低リスク・回答のやり直しを防ぐ。復元後の送信内容・別IDへの切替・画像差分をComposeテスト。永続DB草稿は今回の寿命に対し過大 |
| A04 / P1 / 未修正・コード確認 | `app/.../push/Reply.kt:100`。返信全文+FCM title/bodyをWorkManager Dataへ格納。10KB制限超過でbuild例外がReceiverへ伝播し返信不可 | payload永続化+IDのみDataへ。小さい代替は例外を捕捉しアプリでの返信へ誘導。本文切捨ては不可。中・schema/cleanup設計あり・長文時の効果大。日本語/絵文字/メタデータ込みの境界とReceiverをテスト |
| A05 / P1 / 未修正・コード確認、障害再現未実施 | `app/.../push/Reply.kt:57,70`、`shared/.../data/api/Dto.kt:154`、`gateway/internal/api/server.go:396`。POST実行後の応答紛失で最大4回同じ非冪等送信 | request IDとGateway重複排除を設計。中・プロトコル/再起動境界の回帰リスクあり。単純なretry停止は未送信時の回復を損なう。応答を破棄するHTTP試験でprovider実行回数を検証 |
| A06 / P1 / 未修正・設定確認、過去成果物比較未実施 | `android/app/build.gradle.kts:49`、`.github/workflows/macos-release.yml:45,83`。releaseはdebug鍵で署名しfresh runnerへ同一鍵の復元なし。公開APK間で鍵が変わると更新失敗 | 既存ユーザーの署名鍵とfingerprintを確認し、同一鍵のsecret運用を決める。中・既存配布との互換性が重要。鍵を独断で新規作成しない。2世代APK証明書比較+更新インストール試験 |
| A07 / P2 / 未修正・競合経路確認、端末再現未実施 | `app/.../data/SessionRepository.kt:67,92`。VMのMutexはWorker/Outboxを含まない。古いanchor応答Aが新しい応答Bの後でreplaceFromすると末尾が一時巻戻り | repoのsession別Mutexでanchor読取→通信→保存とreloadを直列化。listにも独立制御。小〜中・待時間増に注意・安定性効果大。Room実DB+遅延HTTPで応答逆転試験。ETag ackだけではこの競合は解消しない |
| A08 / P2 / 未修正・コード確認 | `app/.../ui/detail/SessionDetailRoute.kt:65,72`、`ui/newsession/NewSessionRoute.kt:72,78`。選択添付はrememberのみ、metadata provider例外も未捕捉 | URI/name/MIMEの保存とURI権限寿命を合わせて設計し、読めない添付をエラー表示。小〜中・provider差異あり。回転/再生成/失効URI/provider例外をテスト |
| A09 / P2 / 未修正・コード確認 | `gatewayClient/.../GatewayApi.kt:79,146`。blocking Call.executeはCoroutineキャンセルで通信自体が止まらない。slowPostは長時間継続し得る | cancelをCall.cancelへ伝える共通処理を検討。中・副作用POSTは切断してもサーバー実行を取り消せない点を維持。遅延HTTPの終了時間、FD、キャンセル伝播を測る |
| A10 / P2 / 未修正・テスト構成確認 | `app/build.gradle.kts`、`data/db/Database.kt:145`、`.github/workflows/macos-release.yml:3,69`。Android実DB/Worker/OS復元テストなし、CIはtag/manualのみでPR gateとlintなし | まずPRで既存test+lint、次にRoom1→8 migration/末尾置換/通知返信へ限定してAndroidテスト追加。中・CI時間とエミュレータ安定性のコスト。全層の形式的テストやDI刷新を先行させない |
| A11 / P2 / 未修正・要再確認 | `shared/.../ui/detail/SessionDetailScreen.kt:154,189`。followNewestが再生成でtrueへ戻る。履歴閲覧から次の出力で最新へ飛ぶ可能性 | session key付き保存。小・低リスク。スクロール位置observerとのタイミングを再生成+出力追加テストで確認してから修正 |
| A12 / P2 / 未修正・要計測 | `app/.../ui/files/FileViewerScreen.kt:405`、`shared/.../ui/markdown/MarkdownText.kt:59`、`app/.../data/Settings.kt:38,52`、`HerdrMobileApp.kt:67`。composition中の大量parse/highlight、起動時runBlocking/cache reset | 大規模fixture/通常起動/接続変更起動をPerfetto/Macrobenchmarkで測定。中・順序変更のリスク。問題が測定された箇所だけ非同期化/分割。速度・jank改善率は未測定につき主張しない |
| A13 / P2 / 未修正・要端末確認 | `shared/.../ui/detail/InteractionCard.kt:383` clickable行と操作可能子Checkbox/RadioButton、`ui/newsession/ReorderableChipRow.kt:181` dragのみの並替え | selectable/toggleable semantics集約、移動customActions/keyboard操作。小〜中・操作意味変更に注意。TalkBack/Switch Access/キーボード、200% font、大画面/IMEの実機検証 |
| A14 / P2 / 未修正・API意味からの推論 | `app/.../push/Reply.kt:118`。APPEND_OR_REPLACEで後続返信を既にenqueueした後に先行が失敗すると後続も依存失敗となり得る | WorkManager integration testで確認。中・順序と再試行を壊さないqueue設計が必要。enqueue時点のfailed chain置換と実行中chainの失敗を区別 |
| A15 / P3 / 未修正・警告確認 | `android/gradle.properties:5,6`、`shared/build.gradle.kts:14`、`shared/.../SwipeableSessionRow.kt:67`。AGP9互換設定/旧Kotlin plugin、Android host tests無効、旧swipe API | 独立したGradle/API移行で段階検証。中・広い回帰リスクがあり今回不採用。commonTestはDesktopで実行済みであり、無実行と誤認しない |

パス中の`app/...`は`android/app/src/main/java/com/tohutohu/herdrcompanion/`、`shared/...`は`android/shared/src/commonMain/kotlin/com/tohutohu/herdrcompanion/`、`gatewayClient/...`は同モジュールの`src/main/kotlin/com/tohutohu/herdrcompanion/`を指す。行は今回の修正後。

### セキュリティ・OS/配布に関する判断

- `network_security_config.xml`のHTTP許可はTailscale/信頼LAN対応の仕様。外部HTTPの盗聴リスクは残るが、一律HTTPS化は採用しない。HTTPS/Tailscaleの案内と接続先への警告を必要に応じ検討する。
- `REQUEST_INSTALL_PACKAGES`はダウンロードしたAPKをinstallerへ渡す用途。現在のGitHub配布と将来のPlay配布は要件が異なる。Playへ展開する場合に権限用途/署名/target SDK/審査申告を確認する。今回Play適合を認定していない。
- Prefetch/Replyの短い永続背景処理にWorkManagerは妥当。FCM callbackで長時間処理していない。Android 11以下のexpedited互換foreground service/権限はmerged Manifestに含まれており、欠落という指摘は棄却した。
- Roomの破壊的fallbackを直ちにデータ消失P0とはしない。原本はMac側。ただし未送信草稿/queueを将来Roomへ追加する場合は同じ判断を流用できない。

## 5. 今回実装した3件

| commit | 変更と選定理由 | 限界 |
|---|---|---|
| `3705715` | 添付読込み上限を検査時点より前へ。サイズ不明巨大streamを20MiB+1で拒否し、20MiB以下と既存エラーを維持。依存追加なし | 上限内のByteArrayとcopyは引き続き存在。Android実プロセスのpeak heapは測っていない |
| `62dc143` | `MessageSnapshot` receiptを導入しRoom/表示反映後にack。重要な質問・承認が304で隠れる障害を回避し、304最適化も維持 | refresh同士の逆順書込み、HTTP自体のキャンセルは別課題 |
| `848b935` | 質問回答の選択・自由入力をinteraction単位でrememberSaveable+Saverへ。文字列と選択だけ保存し、DTO/履歴全体は保存しない | Android Activity/OSプロセスでの復元、非常に大きな草稿のBundle容量は別途検証。強制終了後の永続草稿を保証しない |

いずれも通常の送信仕様、データ形式、画面レイアウト、配布先を変更しない。UIテストは既存Desktop Compose基盤を使い、Android向けの新しいテスト製品を追加しなかった。

## 6. 検証記録

### 再実行方法

```bash
cd android
export JAVA_HOME=$HOME/Library/Java/JavaVirtualMachines/jbr-17.0.14/Contents/Home
./gradlew --console=plain :app:assembleDebug :app:testDebugUnitTest :app:lintDebug \
  :shared:allTests :gatewayClient:test :desktopApp:test \
  :app:assembleRelease :app:lintRelease
./gradlew --console=plain :app:analyzeReleaseR8Config

cd ../gateway
env -u GOROOT go test ./...
env -u GOROOT go vet ./...
cd ..
swift test --package-path macos
git diff --check
```

### 成功

- 変更前: Android debug assemble / 全app JVM tests / Lint、shared allTests、Desktop testsが成功。GatewayClientの専用testはNO-SOURCEでありAPIテストはappから実行。
- 変更前: Go全package test（cache利用あり）とvet成功。Swift Manager 6 tests成功。
- 添付7 tests成功: 空/上限同値/1byte超過/サイズ不明巨大入力/short read/zero read/cancel。実際の読み出しバイト数が最大20MiB+1に収まることをassert。
- ETag API tests 16件成功（2件追加）。旧eager-tag保存を一時的に戻すと追加2件が失敗し、修正へ戻すと成功。
- 質問Compose tests 2件成功。旧rememberでは保存・再生成後のAnswer有効性のテストが失敗、修正後は選択/日本語入力をそのまま送信できる。別interaction IDに旧回答を送信しないことも確認。
- 質問再生成前後の画像をpixel一致比較して成功し、生成画像を目視確認。`android/shared/build/reports/audit-ui/question-before-recreation.png`と`question-after-recreation.png`（500×298、Desktop描画）。Androidの実画面スクリーンショットとは区別する。
- `:app:analyzeReleaseR8Config`成功。HTML/PBは`android/app/build/reports/r8/`。独自の広域keepはなく、この監査でkeep削除はしていない。
- 最終全体検証の結果は下の確定欄を参照。

### 失敗と区別

- 最初の監査コマンドはJVMモジュールへ存在しない`:gatewayClient:allTests`を指定してtask選択失敗。ソースの故障ではない。`:gatewayClient:test`、`:desktopApp:test`へ訂正して変更前検証に成功。
- UIの旧状態保存とETagの旧挙動での回帰テスト失敗は、障害検出のための意図的な比較。最終ソースには旧挙動を残していない。
- R8 skillが参照する`scripts/convert_pb_to_json.py`と`scripts/analyze.py`はインストール済みskillに存在せず、両コマンドはファイル不在で失敗。Gradle公式解析は成功したが、skill独自JSON/keep score変換は未生成。scoreやAPK削減率を捏造しない。
- 変更前LintはError 0 / Warning 32 / Hint 1。主に依存更新提案、既知HTTP設定、SDK/Gradle移行など。警告を一括抑制せず、既存構成として記録した。

### 未実施・範囲外

Android instrumented/Room migration実DB、実FCM/通知返信、Activity回転/OSによるプロセスkill、TalkBack/大画面/200% font/Android IME・system barの実画面比較、Perfetto/起動・フレーム・battery計測、公開APK証明書比較、Linux package実行、DMG更新は未実施。
adbには既存エミュレータが接続済みだったが、Android instrumented harnessはなく、利用中設定や実Gatewayに対する操作を避け、共有Composableの独立テストで検証した。測定用の隔離Gateway/sessionとAndroid test harnessを次の開始条件とする。

### 最終検証の確定値

| 対象 | 結果 |
|---|---|
| 最終統合Gradle（上記8種類のtask群） | **BUILD SUCCESSFUL**、2m 8s。app debug/release compile・assemble、R8 minify・resource shrinking、両Lintを含む |
| app JVM | 29 suites / **156 tests** / failures 0 / errors 0 / skipped 0 |
| shared Desktop（commonを含む） | 12 suites / **55 tests** / failures 0 / errors 0 / skipped 0 |
| desktopApp JVM | 18 suites / **84 tests** / failures 0 / errors 0 / skipped 0 |
| gatewayClient:test | NO-SOURCE（APIテストは上記appに含む） |
| Android debug Lint | Error **0** / Warning **32** / Hint **1**。変更前と同数 |
| Android release Lint | Error **0** / Warning **33** / Hint **1**。releaseのみの追加警告は既存ManifestのMonochromeLauncherIcon。変更前release Lintは未測定につき増減は断定しない |
| R8 config analyzer | 成功、21s。skill独自変換は前述の欠落で未実施 |
| Go test / vet | 成功（ソース変更なし、変更前実行） |
| Swift Manager | **6 tests** / failures 0（ソース変更なし） |
| UI画像比較 | 再生成前後500×298画像のpixel一致。PNG SHA-256も両方`cb97122f8db022184f656b0e5e8b92863406abb2a6a614eebf623dcef8c858d9` |
| Release APK | `android/app/build/outputs/apk/release/app-release.apk`、6,312,020 bytes。SHA-256 `0018d4505dcac07abda554a57b75e0402502fbcba73c90ab366387e1667a0522`。debug鍵署名という既存配布方式を維持 |
| 差分レビュー | 修正3件を独立commit。別担当のread-onlyレビューでも重大な回帰なし。`git diff --check`成功。依存/Room schema/本番設定の変更なし |

JVM/Composeの合計は295 tests（追加11件を含む）。Swiftの6件とは区別する。buildの所要時間はcache込みの実行記録であり、変更前後の性能比較ではない。

一時ログは`/private/tmp/herdr-audit-{baseline,final,go,go-vet,swift,r8,ui-before,ui-screenshot}.log`等。Gradle HTML/XMLとPNGはbuild配下の再生成可能な成果物でありcommitしない。再開時は上記コマンドで再生成する。

## 7. ロードマップと再開点

1. **通知返信の信頼性**: A04のData容量エラーを再現し、payload保存方針を選ぶ。同時にA05/A14の送信ID・重複排除・queueの失敗意味を合意し、応答紛失試験を作る。新しい鍵や永続schemaは具体案をレビューしてから扱う。
2. **配布と回帰防止**: A06で既存APKの証明書を比較し更新互換性を確保。A10のPR test/lintを整え、Room migration/Workerの最小Android harnessを導入する。初期候補は既存stackを活かすin-memory RoomとCompose test。
3. **状態競合・入力**: A07の応答逆転を実DBで再現してrepo境界で制御、A08のURI権限と添付復元、A11の履歴追従を限定修正する。
4. **端末品質と測定**: A12/A13を隔離したsession・大規模fixtureで測る。phone/tablet/foldable、gesture/3button、TalkBack、font/IMEを確認。Baseline Profileや追加cacheは測定の結果に基づいて選択する。
5. **移行負債**: A15のAGP10に向けた非推奨設定を独立PRへ。機能修正と同時にライブラリ一括更新しない。

## 公式資料との照合

参照日: 2026-10-15。公式推奨はこのアプリの用途と比較し、層や依存の追加自体を目的にしなかった。

- [Android architecture recommendations](https://developer.android.com/topic/architecture/recommendations): 状態所有・データ層・ライフサイクルに応じたFlow収集の評価。
- [Composeの状態保存](https://developer.android.com/develop/ui/compose/state-saving): 入力/選択の復元と最小状態の保存、Bundle容量の制約。
- [WorkManager Data](https://developer.android.com/reference/androidx/work/Data): A04のシリアライズ容量制限。
- [WorkManagerのwork管理](https://developer.android.com/develop/background-work/background-tasks/persistent/how-to/manage-work): unique workと失敗・依存関係。
- [Androidのアプリ署名](https://developer.android.com/studio/publish/app-signing): A06の署名継続性とdebug keystore。
- [Compose performance](https://developer.android.com/develop/ui/compose/performance/bestpractices)、[semantics](https://developer.android.com/develop/ui/compose/accessibility/semantics): A12/A13の今後の測定・操作検証。

## 8. 追補: 残課題と500 MiB添付への対応（2026-10-15）

この節が現在の状態を示す。1〜7節の件数・commit・20 MiB仕様は初回監査時の履歴である。利用者の追加依頼に基づき、初回の最大3件という実装枠の後に、以下を追加実装した。

### 大きな添付をメモリに積まず送る

AndroidとDesktopの全量ByteArray読み込みを廃止し、共通`StreamingUploadBody`が64 KiB単位でInputStreamから送信する。上限を既存20 MiBから**500 MiB（524,288,000 bytes）**へ変更し、Gatewayの受信上限も揃えた。未知サイズもchunkedで扱い、上限+1 byteまでに停止する。既知サイズの超過は送信前に拒否する。読取り中のサイズ変化、short/zero read、provider例外、キャンセルでもstreamを閉じる。

転送全体の時間枠は30分、書込みが停滞した場合は60秒。one-shot request bodyとして、HTTP redirect/retryで文書URIを勝手に再読込しない。送信を再操作した場合にはsourceを開き直せる。旧`UploadBytes.kt`と旧20 MiB helperテストは利用0を確認して削除し、streamingの境界/HTTPテストに置き換えた。

- Go HTTPテストで500 MiBを実送信し、保存サイズ一致を確認した。上限+1の413と不完全ファイル削除、中断時cleanupも確認。
- JVMでは500 MiBの生成streamを消費し、最大read要求が64 KiBであることを検証。500 MiBをMockWebServerに蓄積して試験する方式は使わない。
- Androidのpicker metadataと保存状態にsizeを引き継ぎ、復元後も既知サイズを活かす。旧triplet形式の保存状態はサイズ不明として復元する。
- Gatewayまでの転送容量であり、個々のエージェントが処理できる形式・画像等の上限を拡張するものではない。

### バックログの現在地

| ID | 追加対応 | 現在の限界 |
|---|---|---|
| A02 | **更新済**: Android/Desktop/Gatewayの500 MiB streaming、実転送/境界テスト | 本番の旧Gatewayも更新が必要。モバイル回線500 MiB実転送や実機peak heapは未測定。通常OutboxはアプリscopeでありOSによるプロセス終了後の転送継続を保証しない |
| A04 | **修正済**: 通知返信本文をprivate fileへatomic保存し、WorkManager DataはUUIDのみ。40,000回の日本語/emojiを含む長文でDataが512bytes未満と本文保持をRobolectric検証 | Notification表示用のpreviewだけ2000文字に制限するが、送信本文は切り捨てない。Gatewayの通常テキストJSON上限1 MiBは別仕様 |
| A05 | **通知返信で修正済**: optional requestId+永続receipt、POST前の対応確認、同じ成功要求は202を返し再実行しない。不確定な実行は409で会話確認へ | exactly-once配送は保証しない。実行前に停止すれば未送信になり得るが、曖昧な送信を重ねない。旧Gatewayには一度送信後の曖昧な自動retryをしない。画面内Outboxの手動retryは従来方式 |
| A06 | **修正・登録済**: 環境変数で固定鍵、CIは未設定時fail-fast。所有者承認後、Mac既存鍵を4つのGitHub Secretsへ登録 | 公開済みの別鍵APKへの上書き互換は復元できない。リモートCIでの実releaseは未実行 |
| A07 | **修正済**: repoのsession別Mutexをpoll/prefetch/outbox/reloadで共有。anchor読取から通信・DB反映まで直列化し、session+message保存をtransactionへ。list refreshも直列化 | セッション一覧と詳細という異なるAPI snapshot間の鮮度をグローバルrevisionで整合する設計ではない |
| A08 | **修正済/範囲限定**: URI/name/MIME/sizeを保存、session/フォームownerを分離、metadata取得失敗を添付単位で表示。キャンセルは再送出 | 永続URI grantは追加しない。同taskの有効grant下での復元を改善。端末再起動等でgrantが失効した場合は再選択が必要 |
| A09 | **修正済**: `Call.awaitBody`のcancelを`Call.cancel`へ接続。APIの受信/送信/長時間POSTのtimeoutを維持して、待機やbody読取中のsocketも閉じる | 呼出しをキャンセルしてもGatewayで始まった副作用は取り消せない。ContentProvider内でopen/read自体がブロック中なら、その呼出しが戻るまでsource closeは遅れ得る。PairingやCoil独自の通信管理まで一律置換はしていない |
| A10 | **改善済**: PR/main品質CIとRobolectricテストを追加。旧Room1〜7→8のデータ保持、tail置換、取得/再読込競合、WorkManager DataをAndroid API35のSQLiteで検証 | 実端末instrumented/実FCM/OS kill試験とCIのリモート実行は残る。テスト用Applicationは実Gatewayへ接続しない |
| A11 | **修正済・再現済**: followNewestをsession別rememberSaveableへ。履歴touch→追加出力→保存/復元→追加出力を実Composeで検証 | 旧実装は最後の「新出力でも履歴維持」assertだけ失敗し、修正で成功。初期fixtureのtouch領域不備とは区別 |
| A13 | **改善済**: 質問行のselectable/toggleable semanticsを集約。チップへcustomActionsとAlt+左右キーを追加し、disabled時は操作させない | TalkBack音声・Switch Access・Androidハードウェアキーボードは未実機確認。gesture既存テストを維持し、semantics boundsも視覚transformと合わせた |
| A14 | **修正済**: UI上の恒久エラーでもWorkの終端はsuccessとして後続chainを阻害しない。payload順序・中断再開・取消・旧Data移行・接続切替を扱う | session毎1枚の通知は、後続成功で先行失敗表示を上書きし得る。失敗本文は30日private保存するが、アプリ内復旧画面は未追加 |
| A12 / A15 | **継続調査**: composition中の大量処理/初期化の実測、AGP10移行は今回の機能修正に混ぜない | 速度向上を推測で断定しない。互換DSL・旧swipe APIの一括移行は別変更で確認する |

Gatewayのreceiptは`state/message-delivery`へbodyのhashとaccepted状態だけ保存し、provider実行前にfile/directoryをfsyncする。成功receiptはatomic置換する。本文は保存しない。遅延retryによる再実行を防ぐためreceiptをTTL削除しない。API契約は[api.md](api.md)、正本/永続状態と旧Gateway互換は[architecture.md](architecture.md)を参照。

通知返信のprivate payloadは成功時削除、旧接続分削除、終端失敗30日保持、offline pendingは期限削除しない。保存→enqueueの隙間でプロセスが終了しても次回起動で再開し、既存Workをtagで照合して二重enqueueを避ける。旧APKの本文入りDataは成否不確定として移行し、勝手に再送しない。

### 署名の実調査と承認された外部操作

GitHub公開APKを取得して`apksigner verify --print-certs`で比較したところ、v1.6.0とv1.6.1は異なる証明書だった。前節の「危険」はこの2成果物では確認済みとなった。
所有者がチャットで「既存Mac鍵をGitHubへ登録する」を承認した後、ローカル鍵からexportした公開証明書が既存ローカルAPKのSHA-256 `4f77a2d3dd622cbd9b598825506f8bf978ef0aafd428fc4ed189969f5ca628a9`と一致することを検証し、4つのRepository Secretsへ登録した。鍵・password・base64をsourceやログに出力していない。登録名は一覧APIで確認した。
既存公開鍵の秘密鍵を復元したわけではなく、過去公開APKの更新互換は別問題である。端末のアンインストール、本番Gateway交換、Herdr再起動、Git push/Release公開はしていない。

### 追加検証・失敗の切り分け

- `:app:testDebugUnitTest :gatewayClient:test :desktopApp:test`成功。追加のRoom/通知/Data/API試験を含む。
- `:shared:allTests`成功。旧gesture/アニメーションテストに加え、添付復元/例外、scroll復元、選択semantics、並替customActions/keyboard/disabledを検証。
- Go全package test/vetと、新規delivery receipt試験のrace検出を実行。
- 独立レビューで「一時的な本文読取り失敗をWork成功と扱い、その後の起動で未送信ファイルを削除する」回復経路を発見。読取り失敗のbounded retry・ファイル保持、残存payloadの再queueとattempted/receipt再判定へ修正し、成功状態と配信成功を混同しない回帰試験を追加した。
- 質問画面のsemantics変更前/後は500×298 pixelsで差分0。PNG SHA-256も同一。PNGの実寸はImageIOで500×298と確認した。
- 最初の複数Gradle実行でshared jar生成が衝突し`zip END header not found`。以降は親の単一実行へ集約した。ソース不具合と区別する。
- 追加テストのcompile/API呼出し誤り、JUnitの非Unit戻り値を修正。初期scroll fixtureはoverlay領域へtouchしていたため、transcriptを操作するfixtureへ修正してから旧実装の障害を確認した。
- 新semanticsの位置がgraphicsLayerより外にあってboundsがドラッグ位置を追わない回帰を既存2テストが検出。コードのmodifier順を修正し、既存matcherや閾値を緩めず成功した。
- Python環境にPillowがないため画像比較はJDK ImageIOで実行。画像内容を生成/改変せずpixelを比較した。
- 固定署名経路を指定して最終releaseを生成し、ローカルAPKとの証明書一致も確認する。最終値は下記確定欄へ記録する。

| 最終チェック | 確定結果 |
|---|---|
| 固定署名経路を指定した統合Gradle | **BUILD SUCCESSFUL**、1m 9s。debug/release assemble、app/shared/client/Desktop tests、両variant Lint、R8+resource shrinkingを含む |
| app JVM/Robolectric | **169 tests** / failures 0 / errors 0 / skipped 0 |
| shared common/Desktop Compose | **65 tests** / failures 0 / errors 0 / skipped 0 |
| gatewayClient JVM | **13 tests** / failures 0 / errors 0 / skipped 0 |
| desktopApp JVM | **84 tests** / failures 0 / errors 0 / skipped 0 |
| 合計 | **331 tests**。初回295件から旧helper7件を削除し、新しい回帰43件を追加 |
| Debug / Release Lint | Error **0 / 0**、Warning **38 / 39**、Hint **1 / 1**。警告増分6件はUseKtxの提案で、今回一括整理はしていない |
| Go | 全package test/vet成功、receiptの新規8試験を含むAPI race検査成功 |
| Mac Gateway artifact | ad-hoc署名のapp/DMGを生成しcodesign --verify --deep --strict成功。Developer ID署名・notarizationではない |
| APK | `android/app/build/outputs/apk/release/app-release.apk`、6,344,788 bytes、SHA-256 `06313dbbfab0435b85220f2273292d3a0de65f41b143a10737b640fcf6b4831b` |
| APK証明書 | `apksigner verify --print-certs`成功。SHA-256 `4f77a2d3dd622cbd9b598825506f8bf978ef0aafd428fc4ed189969f5ca628a9`、承認されたMac既存鍵と一致 |
| Gateway DMG | `macos/build/Herdr-Companion-Gateway-arm64.dmg`、12,652,256 bytes、SHA-256 `6c4884857459cf8a98635608eca6298e625c464c503f7b43ec21831b0a003d73` |
| Workflow / diff | 両workflowをRuby YAMLでparse成功。リモートworkflowは未実行。`git diff --check`成功、独立レビューでも新規P0/P1の指摘なし |

最初の最終実行では、追加修正中の`Reply.kt`を対象にrelease LintのSuspiciousIndentationが1件発生した。ソースを固定しcompile/Lintを再実行してエラー0を確認した。Lint抑制やbaseline追加はしていない。
追加検証ログは`/private/tmp/herdr-followup-{core,ui-2,final,final-2,go,go-vet,gateway-build}.log`等。最終成功は`final-2.log`。画像は`android/shared/build/reports/audit-ui/`、HTML/XMLは各moduleのbuild reports/test-resultsに再生成可能。

実装は`7d24478`（500 MiB/HTTP）、`e846e7e`（Room/refresh）、`60f18b7`（返信/receipt）、`e8c169c`（UI状態/操作）、`590de52`（品質CI/署名）に分割してcommitした。既存未追跡skillsは変更・commitしていない。


### 再開点

まずこの節の確定結果と最新commitを確認する。500 MiB利用にはAndroid/DesktopのclientとGateway双方が必要。ローカルGateway app/DMGとAPKを生成しているが、稼働中の`/Applications`は置換していない。変更を実サービスへ入れる段階と、本番の送信/通知操作は分けて扱う。
残る主な作業はAndroid OS実動作/URI権限寿命、通知失敗本文のアプリ内復旧UI、画面内Outboxへの同じ送信IDの活用、性能の実測、大画面/補助技術の実機評価、AGP移行である。


## 9. 追補: 通常送信の再送とAndroid OS検証（2026-10-15）

依頼「2,4あたり」に対応。前回の優先順位2（通常Outboxの重複送信）と4（Androidプラットフォーム上の検証）を進めた。500 MiBの添付上限とstreamingは維持している。

### 9.1 通常送信の正確性（P1、修正済み）

根本原因はAndroid OutboxとDesktopが初回/手動retryを別のPOSTとして送り、通知返信で導入したreceipt/requestIdを使っていなかったこと。受付後に応答だけ失われると、再送が同じ指示をもう一度providerへ渡す。AndroidのRetryは状態確認なしでjobを増やし、Desktopは成功済み添付も再uploadしていた。コード経路と実HTTPの応答切断で確認した。

- `gatewayClient/.../api/MessageDeliveryAttempt.kt`: enqueue時のlocalId/接続先、本文、session、成功uploadIdsを1回の論理送信に固定する。receipt対応を確認したGatewayへ同requestIdを渡し、再試行時も同じJSONを送る。接続先変更、対応の後退、409では曖昧な再送を停止する。
- `gatewayClient/.../api/GatewayApi.kt`: message POSTだけone-shot bodyにし、OkHttp内部の自動replayも止める。旧GatewayはrequestIdを無視し得るため、送信後の不確定な結果をクライアントから重ねない。通信前/upload段階の失敗は再試行できる。
- `app/.../data/Outbox.kt`: FAILED→SENDINGのCASで二重Retryを防ぐ。UNDISPATCHEDでsession Mutexの待機列へ入り、dispatcherの実行順で指示が逆転するのを防ぐ。
- `desktopApp/.../DesktopAppState.kt`: 同helper/添付ID保持とsession FIFOを使う。排他制御を追加した最初の実装ではFAILED表示直後のRetryがprivate lockに拒否される競合を回帰試験で検出し、lock解放後にFAILEDを公開するよう修正した。
- `shared/.../detail/PendingMessageViews.kt`: 結果不明を「Not sent」と断定せず、会話確認を案内しRetryを無効化する。履歴に届いたメッセージとの既存照合は維持する。

既存RepositoryとHTTP clientを使い、新しい永続schema/本番依存は導入していない。修正リスクは送信経路に限定される。旧Gatewayでは再送を保守的に止めるが、重複指示を防ぐための意図した挙動。provider実行そのもののexactly-onceや、通常Outboxのprocess kill後の再開を保証する変更ではない。commit: `50bf43c`。

追加回帰は16件: Android4、gatewayClient7、Desktop3、共通UI2。受付後の応答切断でもprovider相当の副作用1回、同ID/本文/添付、upload1回、二重tap、FIFO、legacy停止、307自動replay停止、接続切替、409、対応後退を検証する。

主な修正位置（repo相対、今回の最終source）:

| ファイル | 行/責務 |
|---|---|
| `android/gatewayClient/src/main/kotlin/com/tohutohu/herdrcompanion/data/api/MessageDeliveryAttempt.kt` | 12: 論理送信のidentity/接続固定とretry可否 |
| `android/gatewayClient/src/main/kotlin/com/tohutohu/herdrcompanion/data/api/GatewayApi.kt` | 188: one-shot message POST |
| `android/app/src/main/java/com/tohutohu/herdrcompanion/data/Outbox.kt` | 79: retryのCAS、114以降: session直列配送 |
| `android/desktopApp/src/main/kotlin/com/tohutohu/herdrcompanion/desktop/DesktopAppState.kt` | 810: 初回/retry共通配送と排他 |
| `android/shared/src/commonMain/kotlin/com/tohutohu/herdrcompanion/ui/detail/PendingMessageViews.kt` | 115/122: 結果不明表示とRetry無効化 |

### 9.2 Androidテストの隔離と再現性

Google公式 `testing-setup` / `android-cli` skillを使用し、[instrumented testing](https://developer.android.com/training/testing/instrumented-tests)、[ActivityScenario](https://developer.android.com/reference/androidx/test/core/app/ActivityScenario)を参照した。手動DIと既存JUnitを維持し、テストのためのDI frameworkは追加していない。

`deviceTest` variantはapplicationId suffix `.devicetest` を持ち、instrumentedコードはそのsuffixを権限操作・Settings/DB変更より先に検証する。通常debugのJVM/CI taskは維持し、`-PdeviceTests=true`の時だけこのテストを有効にする。deviceTest専用host Activityは非exported、adb設定ReceiverはDUMP保護。通常APKには含めない。

実行は `scripts/run-android-device-tests.sh` に集約し、serial必須・ゼロ件成功防止・失敗ログ保存・スクリーンショット回収を行う。CIは専用テストAPKのコンパイルも行う。[詳しい再現手順](android-device-testing.md)を追加した。端末内MockWebServerと実WorkManager/Room/Android Activity/PendingIntentを使い、実利用のHerdr/providerに接続しない。

初回Android37のテストは、Compose経由のEspresso3.5.0が削除済みInputManager.getInstanceを呼び7件ともテスト基盤内で失敗した。実アプリの障害とは区別し、[公式修正情報](https://developer.android.com/jetpack/androidx/releases/test#espresso-3.7.0)とローカルAARを確認して、instrumented依存に修正済み3.7.0を明示した。SDK37の初期起動ではSystem UI等にもANRが起こり、最初の実行は中断した。テスト件数や成功と混同しない。

### 9.3 検証の確定結果

変更前の既知基準は追補8のJVM331件green。今回の最終統合では、追加16件を含む347件が全成功した。新規test harnessの失敗は下に分離している。

| 対象 | 確定結果 |
|---|---|
| JVM | app **173**、gatewayClient **20**、Desktop **87**、shared **67**。計 **347件**、failure/error/skipped 0 |
| Android instrumented | 専用Google Play ARM64/Android37 AVDで **7件全成功**、49.464s。別途、WorkManager履歴を消さずchain試験を再実行して **1件成功**（3.855s、件数に重複加算しない） |
| Activity/Compose | 日本語・絵文字・複数選択の回答、添付metadata、履歴位置を実Android Bundle経由で再生成後に復元。履歴閲覧中の追加入力でも最新へ飛ばない |
| Android通知/Worker | 合成SDK push→実Service→PrefetchWorker→Room→実通知PendingIntent→詳細→Activity再生成。長文RemoteInput→実ReplyWorker→HTTP→再取得Roomの本文一致。503の実backoffと同ID再試行、先行400時の後続chain成功 |
| 本物のFCM/background | private fixtureに専用端末1台だけ登録し、現ソースをbuildしたCLIから送信。background通知Record（completed_v2/importance4）とHTTP先読みを確認 |
| 本物のFCM/process | backgroundの `am kill` 後、pid消失を確認。次のFCMで新processが起動して先読み成功。force-stopの代用ではない。OSの自然なLMKや送信中killの保証とは区別 |
| 本物のFCM/Doze | `mScreenOn=false` / `mCharging=false` / `mState=IDLE` を確認し、高優先度FCMから先読み成功。強制IDLEであり、自然な長時間Doze/OEM省電力の検証ではない |
| 本物のFCM/回線断 | wifi/data両方を無効にし `Active default network: none` を確認。FCM送信後5秒間HTTP取得数は不変、回線復帰後に取得成功 |
| 実DB保存 | FCM試験後の専用アプリのRoom DB/WALをprivate directoryへ取得し、session `test:notification` と本文 `Isolated push transcript` の保存をSQLiteで確認。FCM試験のHTTP取得計5、message POST 0 |
| Lint | Debug **Error0 / Warning38**、Release **Error0 / Warning39**。前回確定値と同数。test依存はversion catalogへ登録し、新たなUseTomlInstead警告を残していない |
| Release | `:app:assembleRelease`成功。R8 minify/resource shrinkingとも有効。最終統合GradleはBUILD SUCCESSFUL（4m34s、cache込み、アプリ性能比較ではない） |
| 署名/隔離 | APKの証明書SHA256は固定Mac鍵 `4f77a2d3dd622cbd9b598825506f8bf978ef0aafd428fc4ed189969f5ca628a9`。aapt2でrelease ManifestにDeviceTest/DebugConfigReceiver/.devicetestが無いことを確認 |
| 画像 | 実端末から質問・履歴の2PNGを回収して目視確認。日本語/選択/過去履歴が見え、composerとも重ならない。今回の変更前とのpixel比較は未実施。Retryの有効/無効と文言は共通UI回帰試験で確認 |
| CI/差分 | 既存CIのJVM/Lint/releaseに専用テストAPK compileを追加。ローカルで専用compile/実行スクリプト成功、`git diff --check`成功。remote CI実行は未実施 |
| Go/Swift | 今回production変更なし。隔離Gateway CLIの `go build` 成功。Go/Swift testは前追補の結果を維持し今回は再実行していない |

途中で検出・修正した失敗: 新variantの無条件testBuildTypeで既存testDebugUnitTestが消える構成をopt-inへ変更、Robolectricの非generic getApplication呼出しを訂正、前述のDesktop Retry競合を修正。端末harnessはEspresso3.5.0/API37不整合、通知によるMAIN→VIEW変更をActivityScenarioが追跡できない不整合、固定接続IDによるWorkManager履歴件数の混入を順に修正した。最終7件と履歴保持後の再試験は成功しており、appの既存障害として報告しない。初回のSystem UI等のANR、Android36の新規AVD起動未完了は環境上の中断で、API36の実行結果はない。最終の実測プラットフォームはAndroid37のみ。

APK: `android/app/build/outputs/apk/release/app-release.apk`、**6,361,172 bytes**、SHA256 `8cef5f4f8ab2413e5e46e18eb5dfeb0a4c9940fa4d3c74e608a692cb7fee55d8`。

再生成可能な成果物は `android/app/build/reports/device-tests/`（instrumentation.log、fcm-results.json、2PNG）、Gradleの各test-results/Lint report。実行ログは `/private/tmp/herdr-final-checks.log`、`/private/tmp/herdr-instrumented-passed.log`、`/private/tmp/herdr-chain-repeat.log`。実FCMは `scripts/android-device-fixture.py` / `scripts/configure-android-device-test.py` と前述の[手順](android-device-testing.md)で再開できる。fixtureはpush/prefetch専用でmessage送信は501、idempotencyはfalseを返す。通常アプリ/実Gatewayのconfig/実Herdr sessionは操作せず、private一時config・専用AVDだけを使用した。終了時に強制Doze/battery設定を戻し、専用エミュレータとfixtureを停止、専用AVD・一時token/config・DB snapshotを削除した（実行中の他5台のエミュレータには操作しない）。本番アプリ差替、公開release、git pushは実行していない。

未検証: 実機/OEM、Android29〜36での実動作、自然な長時間Doze、送信途中のOS kill、大容量uploadの実機メモリ、文書providerごとのURI grant/revoke・寿命、TalkBack/大画面/200%font、release APKの端末上の操作。端末上の今回のテストはdebug相当の隔離variantであり、releaseはR8 build/Lint/署名までを検証した。

### 9.4 更新後の残課題

1. P1: 大容量の通常送信はアプリscopeのため、OSによるprocess kill後のupload/送信継続は未対応。永続OutboxとURIの扱い、foreground transferの要否を設計する。
2. P1/P2: 通知返信の失敗本文はprivate保存されるが、アプリ内の一覧・コピー・明示再試行UIは未追加。後続通知による上書きもこのUIで回復できるようにする。
3. P2: Androidプラットフォーム検証は下の確定範囲に限る。OEM省電力、実機radio/自然な長時間Doze、提供元別URI権限寿命、TalkBack/フォント拡大/大画面を継続する。
4. P2: 起動/フレーム/500 MiB時peak heap・batteryのPerfetto等による計測。今回のbuild/test所要時間をアプリ性能の改善指標として扱わない。
5. P2: AGP10に向けたDSL/built-in Kotlin移行と公開配布の運用検証。依存の一括更新や既存ユーザーデータの破壊的移行は行っていない。
