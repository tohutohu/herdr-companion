#!/usr/bin/env python3
"""Loopback-only Android device fixture; state directory must be private.

Never points at Herdr or a real agent. Captured FCM tokens remain in mode-600
scratch config for the existing Gateway notify-test command, never stdout.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import unquote, urlsplit

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--state-dir", required=True, type=Path)
parser.add_argument("--firebase-config", type=Path, help="Existing Gateway config; reads only firebaseAndroid")
parser.add_argument("--credentials", type=Path, help="Service account path, used only by notify-test")
args = parser.parse_args()
args.state_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
if args.state_dir.stat().st_mode & 0o077:
    parser.error("state directory must be mode 700")
os.umask(0o077)
token = secrets.token_hex(32)
settings = {"token": token, "gatewayId": hashlib.sha256(token.encode()).hexdigest(), "connectionId": "device-audit"}
if args.firebase_config:
    settings["firebase"] = json.loads(args.firebase_config.read_text()).get("firebaseAndroid")
config = {"authToken": token, "devices": [], "fcmCredentialsFile": str(args.credentials or "")}
messages = []
counts = {"devices": 0, "messagesGet": 0, "posts": 0}
session = {"id": "test:notification", "provider": "claude", "title": "Isolated device audit", "project": "Device audit", "status": "completed", "updatedAt": "2026-10-15T00:00:00Z", "canSend": True}

def save(name, value):
    path = args.state_dir / name
    temporary = path.with_suffix(".tmp")
    temporary.write_text(json.dumps(value))
    temporary.chmod(0o600)
    temporary.replace(path)

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *unused):
        pass

    def reply(self, status, body):
        encoded = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)

    def do_GET(self):
        path = unquote(urlsplit(self.path).path)
        if path == "/__status":
            return self.reply(200, counts)
        if self.headers.get("Authorization") != "Bearer " + token:
            return self.reply(401, {})
        if path == "/v1/sessions":
            return self.reply(200, {"sessions": [session]})
        if path == "/v1/sessions/test:notification":
            return self.reply(200, session)
        if path == "/v1/sessions/test:notification/messages":
            counts["messagesGet"] += 1
            save("counts.json", counts)
            return self.reply(200, {"session": session, "messages": [{"id": "initial", "role": "assistant", "timestamp": "2026-10-15T00:00:00Z", "blocks": [{"type": "text", "text": "Isolated push transcript"}]}] + messages})
        if path == "/v1/message-delivery":
            return self.reply(200, {"idempotency": False})
        return self.reply(404, {})

    def do_POST(self):
        if self.headers.get("Authorization") != "Bearer " + token:
            return self.reply(401, {})
        length = int(self.headers.get("Content-Length", "0"))
        if length > 1024 * 1024:
            return self.reply(413, {})
        body = json.loads(self.rfile.read(length) or b"{}")
        path = unquote(urlsplit(self.path).path)
        if path == "/v1/devices":
            config["devices"] = [{"name": "isolated-device-audit", "fcmToken": body["fcmToken"], "registeredAt": "2026-10-15T00:00:00Z"}]
            counts["devices"] = 1
            save("gateway-config.json", config)
        elif path.endswith("/messages"):
            counts["posts"] += 1
            save("counts.json", counts)
            return self.reply(501, {"error": "This fixture tests push/prefetch, not message delivery"})
        save("counts.json", counts)
        self.reply(202, {"ok": True})

server = HTTPServer(("127.0.0.1", 0), Handler)
settings["gatewayUrl"] = "http://10.0.2.2:" + str(server.server_port)
save("settings.json", settings)
save("gateway-config.json", config)
save("counts.json", counts)
print("Isolated fixture listening on loopback port", server.server_port, flush=True)
server.serve_forever()
