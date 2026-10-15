#!/usr/bin/env python3
"""Configure only the isolated APK, without printing its scratch token."""
import argparse
import json
from pathlib import Path
import shlex
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--serial", required=True)
parser.add_argument("--settings", required=True, type=Path)
args = parser.parse_args()
settings = json.loads(args.settings.read_text())
command = ["am", "broadcast", "-n", "com.tohutohu.herdrcompanion.devicetest/com.tohutohu.herdrcompanion.test.DeviceTestConfigReceiver"]
for key, extra in [("gatewayUrl", "gateway_url"), ("token", "token"), ("connectionId", "connection_id"), ("gatewayId", "gateway_id")]:
    command += ["--es", extra, settings[key]]
if settings.get("firebase"):
    command += ["--es", "firebase", json.dumps(settings["firebase"], separators=(",", ":"))]
# adb shell joins arguments itself, so explicitly quote the complete command.
subprocess.run(["adb", "-s", args.serial, "shell", shlex.join(command)], check=True)
