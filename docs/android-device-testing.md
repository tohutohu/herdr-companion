# Androidの隔離端末テスト

通常アプリとは別の `com.tohutohu.herdrcompanion.devicetest` を使う。既存のペアリング、通知、DBを上書きしない。使い捨てエミュレータを推奨し、serialは必ず明示する。JDK 17とAndroid SDK 37が必要。

```bash
export JAVA_HOME=$HOME/Library/Java/JavaVirtualMachines/jbr-17.0.14/Contents/Home
HERDR_DEVICE_TEST_SERIAL=emulator-XXXX bash scripts/run-android-device-tests.sh
```

7件を実行する。質問回答・添付メタデータ・履歴位置のActivity再生成、SDKへの合成push→Service→PrefetchWorker→Room→通知タップ、長文RemoteInput→実WorkManager→HTTP、503のbackoff再試行、先行400が後続返信を阻害しないことを確認する。HTTPは端末内MockWebServerであり、実Herdr/providerに送信しない。

`ActivityScenario.recreate()`はOSのプロセスkillとは異なる。503は回線断とは異なり、SDKへの合成pushはGoogleからの配送確認ではない。スクリーンショットは `android/app/build/reports/device-tests/` に回収する。標準CIはJVM/Lintと、この専用APK/テストのコンパイルを行う。エミュレータ実行は上の明示コマンドで行う。

## 実FCM・background・Doze・process kill

専用エミュレータと専用APKだけを対象にする。以下のfixtureはloopbackにだけbindし、Herdrやproviderを呼ばない。既存設定から読むのはFirebaseクライアント設定だけ。サービスアカウントは既存Gatewayの `notify-test` にだけパスで渡す。tokenを含む一時ファイルはprivate directory/mode 600で保存し、終了時に削除する。

```bash
task_device_state=$(mktemp -d /private/tmp/herdr-device-fixture.XXXXXX)
python3 scripts/android-device-fixture.py --state-dir "$task_device_state" \
  --firebase-config "$HOME/.config/herdr-mobile/desktop/config.json" \
  --credentials "$HOME/.config/herdr-mobile/desktop/firebase-service-account.json"
# 別ターミナルで同じtask_device_stateを指定
python3 scripts/configure-android-device-test.py --serial emulator-XXXX \
  --settings "$task_device_state/settings.json"
adb -s emulator-XXXX shell pm grant com.tohutohu.herdrcompanion.devicetest android.permission.POST_NOTIFICATIONS
adb -s emulator-XXXX shell am start -n com.tohutohu.herdrcompanion.devicetest/com.tohutohu.herdrcompanion.MainActivity
adb -s emulator-XXXX shell input keyevent KEYCODE_HOME
```

`counts.json` のdevicesが1になってから、**一時configだけ**を指定して送る。通常利用中configを `notify-test` に指定しない（全登録端末へ送られる）。最新ソースのCLIを一時directoryへビルドして使う。

```bash
(cd gateway && env -u GOROOT go build -o "$task_device_state/gateway" ./cmd/herdr-mobile-gateway)
HERDR_MOBILE_CONFIG="$task_device_state/gateway-config.json" "$task_device_state/gateway" notify-test
```

background時の通知表示、`counts.json` のmessagesGet増加を確認する。プロセス再起動試験はbackground後の `am kill com.tohutohu.herdrcompanion.devicetest` でpid消失を確認してから送る。`force-stop`はFCM受信を止めるため、プロセスkillの代用にしない。

強制Dozeは専用エミュレータだけで `dumpsys battery unplug` → `input keyevent KEYCODE_SLEEP` → `dumpsys deviceidle force-idle` とし、IDLEを確認してから送る。高優先度FCMでの通知表示とprefetchは別々に確認する。終了時は必ず `dumpsys deviceidle unforce`、`dumpsys battery reset`、wakeを行う。これは自然な長時間Dozeの再現ではない。

ネットワークを落とす試験も専用デバイスだけを使い、復帰後に通知・取得・再試行を確認する。OEM固有の省電力、実機radio、URI権限の提供元別寿命は別途必要。今回の実測結果と未検証範囲は [engineering-audit.md](engineering-audit.md) の最新追補を参照。
