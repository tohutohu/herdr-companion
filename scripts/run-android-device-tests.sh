#!/usr/bin/env bash
set -euo pipefail

# Explicit serial and a separate package protect a developer's normal pairing.
: "${HERDR_DEVICE_TEST_SERIAL:?Set the serial of a test emulator explicitly}"
task_root=$(cd "$(dirname "$0")/.." && pwd)
mkdir -p "$task_root/android/app/build/reports/device-tests"
task_log="$task_root/android/app/build/reports/device-tests/instrumentation.log"
cd "$task_root/android"
./gradlew --console=plain -PdeviceTests=true :app:assembleDeviceTest :app:assembleDeviceTestAndroidTest
adb -s "$HERDR_DEVICE_TEST_SERIAL" install -r app/build/outputs/apk/deviceTest/app-deviceTest.apk
adb -s "$HERDR_DEVICE_TEST_SERIAL" install -r app/build/outputs/apk/androidTest/deviceTest/app-deviceTest-androidTest.apk
adb -s "$HERDR_DEVICE_TEST_SERIAL" shell am instrument -w -r \
    com.tohutohu.herdrcompanion.devicetest.test/androidx.test.runner.AndroidJUnitRunner | tee "$task_log"
# `am instrument` can exit zero even when a test fails.
if ! rg -q '^OK \([1-9][0-9]* tests?\)' "$task_log"; then
    echo "Instrumented tests did not report success" >&2
    exit 1
fi
mkdir -p app/build/reports/device-tests
adb -s "$HERDR_DEVICE_TEST_SERIAL" pull \
    /sdcard/Android/data/com.tohutohu.herdrcompanion.devicetest/files/device-test-screenshots \
    app/build/reports/device-tests/
