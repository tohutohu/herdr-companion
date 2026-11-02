#!/usr/bin/env python3
"""Measures how often the Gateway's skills trigger on realistic requests.

  trigger.py --agent claude --model haiku --runs 3 [--plugin DIR] [--split val]
  trigger.py --agent codex --model gpt-6-luna

Each run stops as soon as a skill loads or the agent is done, and cannot act:
hc points at a dead address, Claude has no Bash, editing or MCP, and Codex has no
MCP and only its sandbox: writes stay in the empty scratch repository both run in,
without network. A read-only sandbox would skew Codex, which then skips skills for
work it could not carry out.
See https://agentskills.io/skill-creation/optimizing-descriptions.
"""
import argparse
import concurrent.futures as futures
import json
import os
import pathlib
import re
import subprocess
import tempfile
import threading

HERE = pathlib.Path(__file__).resolve().parent
SAFE_ENV = {"HC_URL": "http://127.0.0.1:1", "HC_TOKEN": "eval", "ENABLE_CLAUDEAI_MCP_SERVERS": "false"}
RUN_SECONDS = 150  # a run that took this long decided without a skill
DENIED = ["Bash", "Edit", "Write", "NotebookEdit", "Agent", "WebFetch", "WebSearch", "Workflow", "CronCreate", "ScheduleWakeup", "RemoteTrigger"]


def env():
    drop = ("CLAUDE", "CODEX_THREAD", "HC_", "HERDR_")
    e = {k: v for k, v in os.environ.items() if not k.startswith(drop) or k == "CLAUDE_CONFIG_DIR"}
    return e | SAFE_ENV


def run_claude(query, model, plugin, cwd):
    cmd = ["claude", "-p", query, "--model", model, "--no-session-persistence", "--output-format", "stream-json",
           "--verbose", "--max-turns", "3", "--strict-mcp-config", "--mcp-config", '{"mcpServers":{}}',
           "--disallowedTools", *DENIED]
    if plugin:
        cmd += ["--plugin-dir", str(plugin)]
    p = subprocess.Popen(cmd, cwd=cwd, env=env(), stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                         stderr=subprocess.DEVNULL, text=True)
    found = None
    threading.Timer(RUN_SECONDS, p.kill).start()
    try:
        for line in p.stdout:
            try:
                ev = json.loads(line)
            except ValueError:
                continue
            if ev.get("type") != "assistant":
                continue
            for b in ev["message"].get("content", []):
                if b.get("type") == "tool_use" and b.get("name") == "Skill":
                    name = b["input"].get("skill", "")
                    if name.startswith("herdr-companion:") or name in SKILL_ALIASES:
                        found = name.split(":")[-1]
                        return found
    finally:
        p.kill()
        p.wait()
    return found


SKILL_ALIASES = set()


def forget_codex_trust(cwd):
    """Removes the trust entry Codex adds for a workspace-write thread in cwd."""
    config = pathlib.Path(os.environ.get("CODEX_HOME", pathlib.Path.home() / ".codex"), "config.toml")
    if config.exists():
        block = f'[projects."{cwd}"]\ntrust_level = "trusted"\n\n'
        config.write_text(config.read_text().replace(block, ""))


def run_codex(query, model, effort, skills_dir, cwd):
    p = subprocess.Popen(["codex", "app-server"], cwd=cwd, env=env(), stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                         stderr=subprocess.DEVNULL, text=True)

    threading.Timer(RUN_SECONDS, p.kill).start()

    def send(msg):
        p.stdin.write(json.dumps(msg) + "\n")
        p.stdin.flush()

    def call(i, method, params):
        send({"id": i, "method": method, "params": params})
        for line in p.stdout:
            msg = json.loads(line)
            if msg.get("id") == i and "method" not in msg:
                return msg
        raise RuntimeError("app-server closed")

    try:
        call(1, "initialize", {"clientInfo": {"name": "skill_eval", "version": "0"}, "capabilities": {"experimentalApi": True}})
        send({"method": "initialized", "params": {}})
        call(2, "skills/extraRoots/set", {"extraRoots": [str(skills_dir)]})
        t = call(3, "thread/start", {"cwd": cwd, "model": model, "ephemeral": True, "sandbox": "workspace-write",
                                     "approvalPolicy": "never", "config": {"mcp_servers": {}}})
        tid = t["result"]["thread"]["id"]
        send({"id": 4, "method": "turn/start", "params": {"threadId": tid, "effort": effort,
                                                           "input": [{"type": "text", "text": query}]}})
        pat = re.compile(r"skills/([a-z-]+)/SKILL\.md")
        # Codex has a shell here, so the description alone may lead it straight
        # to hc. That is the right tool without the skill's gotchas: reported apart.
        direct = re.compile(r"(?:^|[\s;&|'\"(])hc (resident )?[a-z]")
        for line in p.stdout:
            msg = json.loads(line)
            method = msg.get("method", "")
            if method in ("item/started", "item/completed"):
                m = pat.search(line)
                if m and str(skills_dir) in line.replace("\\/", "/"):
                    return m.group(1)
                item = msg["params"].get("item", {})
                d = direct.search(item.get("command") or "") if item.get("type") == "commandExecution" else None
                if d:
                    return "direct:" + ("schedule-resident-tasks" if d.group(1) else "drive-sessions")
            if method == "turn/completed":
                return None
    except (BrokenPipeError, ValueError):
        return None  # killed by the timer
    finally:
        p.kill()
        p.wait()
    return None


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--agent", choices=["claude", "codex"], default="claude")
    ap.add_argument("--model", default="haiku")
    ap.add_argument("--effort", default="low", help="Codex only")
    ap.add_argument("--runs", type=int, default=3)
    ap.add_argument("--plugin", default=str(HERE.parent / "plugin"))
    ap.add_argument("--split", choices=["train", "val"])
    ap.add_argument("--jobs", type=int, default=4)
    ap.add_argument("--map", default="", help="old=new skill names, comma separated; '=' alternatives with |")
    args = ap.parse_args()
    plugin = pathlib.Path(args.plugin).resolve()
    mapping = {}
    for pair in filter(None, args.map.split(",")):
        old, new = pair.split("=")
        mapping[old] = new.split("|")
    queries = json.loads((HERE / "trigger_queries.json").read_text())
    if args.split:
        queries = [q for q in queries if q["split"] == args.split]

    with tempfile.TemporaryDirectory() as tmp:
        tmp = os.path.realpath(tmp)
        subprocess.run(["git", "init", "-q", tmp], check=True)
        pathlib.Path(tmp, "README.md").write_text("# scratch\n")

        def one(q):
            if args.agent == "claude":
                return run_claude(q["query"], args.model, plugin, tmp)
            return run_codex(q["query"], args.model, args.effort, plugin / "skills", tmp)

        with futures.ThreadPoolExecutor(args.jobs) as pool:
            jobs = [(q, pool.submit(one, q)) for q in queries for _ in range(args.runs)]
            results = {}
            for q, job in jobs:
                try:
                    got = job.result()
                except Exception as e:  # a broken run counts as not triggered, visibly
                    print("run failed:", e)
                    got = None
                results.setdefault(q["query"], []).append(got)
        if args.agent == "codex":
            forget_codex_trust(tmp)

    passed = {"train": [0, 0], "val": [0, 0]}
    for q in queries:
        got = results[q["query"]]
        hits = 0
        for g in got:
            names = mapping.get(g, [g.removeprefix("direct:") if g else g])
            hits += (q["expect"] in names) if q["expect"] else (g is not None)
        rate = hits / len(got)
        ok = rate > 0.5 if q["expect"] else rate < 0.5
        passed[q["split"]][0] += ok
        passed[q["split"]][1] += 1
        seen = ",".join(sorted({str(g) for g in got}))
        print(f"{'ok ' if ok else 'NG '} {q['split']:5} {rate:4.2f} want={q['expect'] or '-':24} got={seen:32} {q['query']}")
    for s, (ok, n) in passed.items():
        if n:
            print(f"{s}: {ok}/{n}")


if __name__ == "__main__":
    main()
