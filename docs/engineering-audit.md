# Androidプロジェクト品質監査

監査日: 2026-10-15。基準コミット: `1104f86`（1.6.1）。Androidに加え、その共通UI・HTTPクライアント・Desktop利用側と、Gateway/Managerの信頼境界を調査した。
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
- 質問再生成前後の画像をpixel一致比較して成功し、生成画像を目視確認。`android/shared/build/reports/audit-ui/question-before-recreation.png`と`question-after-recreation.png`（500×299、Desktop描画）。Androidの実画面スクリーンショットとは区別する。
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
| UI画像比較 | 再生成前後500×299画像のpixel一致。PNG SHA-256も両方`cb97122f8db022184f656b0e5e8b92863406abb2a6a614eebf623dcef8c858d9` |
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
