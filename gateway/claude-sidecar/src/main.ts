// herdr-claude-sidecar runs Claude Code sessions for the Herdr Companion
// Gateway through the official Claude Agent SDK.
//
// It is a child of the gateway and talks to it over stdio, one JSON object
// per line:
//
//   gateway → sidecar  {"id": 1, "method": "start", "params": {...}}
//   sidecar → gateway  {"id": 1, "result": {...}} or {"id": 1, "error": {"message": "..."}}
//   sidecar → gateway  {"event": "state", "sessionId": "...", "data": {...}}
//
// Each session is one query() in streaming input mode, so messages, model,
// effort and permission-mode changes, interrupts and permission answers all
// go to the same live Claude Code process. Claude Code keeps writing its own
// transcript (~/.claude/projects/…/<session>.jsonl), which stays the history
// the gateway reads. When stdin closes (the gateway exited) every session is
// closed, which ends their Claude Code processes, and the sidecar exits.

import {
  query,
  type CanUseTool,
  type PermissionMode,
  type PermissionResult,
  type PermissionUpdate,
  type Query,
  type SDKMessage,
  type SDKUserMessage,
} from "@anthropic-ai/claude-agent-sdk";
import { readFileSync } from "node:fs";
import { residentPermissionHandler, residentResultFields } from "./resident";
import { createInterface } from "node:readline";

const VERSION = "1";

type Json = Record<string, unknown>;

function write(obj: Json) {
  process.stdout.write(JSON.stringify(obj) + "\n");
}

function emit(sessionId: string, event: string, data: Json = {}) {
  write({ event, sessionId, data });
}

function log(msg: string, fields: Json = {}) {
  process.stderr.write(JSON.stringify({ msg, ...fields }) + "\n");
}

// Input is an async iterable fed by "send" requests: the streaming input
// mode the SDK needs for multi-turn sessions and runtime control.
class InputQueue implements AsyncIterable<SDKUserMessage> {
  private items: SDKUserMessage[] = [];
  private waiting: ((r: IteratorResult<SDKUserMessage>) => void) | null = null;
  private ended = false;

  push(m: SDKUserMessage) {
    if (this.ended) throw new Error("session input is closed");
    if (this.waiting) {
      const w = this.waiting;
      this.waiting = null;
      w({ value: m, done: false });
    } else {
      this.items.push(m);
    }
  }

  end() {
    this.ended = true;
    if (this.waiting) {
      const w = this.waiting;
      this.waiting = null;
      w({ value: undefined, done: true });
    }
  }

  [Symbol.asyncIterator](): AsyncIterator<SDKUserMessage> {
    return {
      next: () => {
        const m = this.items.shift();
        if (m) return Promise.resolve({ value: m, done: false });
        if (this.ended) return Promise.resolve({ value: undefined, done: true });
        return new Promise((resolve) => (this.waiting = resolve));
      },
    };
  }
}

type Pending = {
  toolName: string;
  input: Record<string, unknown>;
  suggestions?: PermissionUpdate[];
  resolve: (r: PermissionResult) => void;
};

type Session = {
  id: string;
  q: Query;
  input: InputQueue;
  pending: Map<string, Pending>;
  closed: boolean;
  // Resident runs are unattended: permission requests are denied (and
  // reported) instead of waiting for a user, and the result event carries
  // the final text, structured output and usage.
  resident: boolean;
};

const sessions = new Map<string, Session>();

function session(id: unknown): Session {
  const s = typeof id === "string" ? sessions.get(id) : undefined;
  if (!s || s.closed) throw new Error("session is not running");
  return s;
}

const imageTypes: Record<string, string> = {
  png: "image/png",
  jpg: "image/jpeg",
  jpeg: "image/jpeg",
  gif: "image/gif",
  webp: "image/webp",
};

// userMessage builds a prompt with the images attached as content blocks,
// the way the Claude Code TUI attaches pasted images.
function userMessage(text: string, images: string[]): SDKUserMessage {
  const content: Json[] = [];
  for (const path of images) {
    const ext = path.split(".").pop()?.toLowerCase() ?? "";
    const mediaType = imageTypes[ext];
    if (!mediaType) {
      text = (text + "\n" + path).trim();
      continue;
    }
    content.push({
      type: "image",
      source: { type: "base64", media_type: mediaType, data: readFileSync(path).toString("base64") },
    });
  }
  if (text) content.push({ type: "text", text });
  if (content.length === 0) throw new Error("empty message");
  return { type: "user", message: { role: "user", content } as never, parent_tool_use_id: null };
}

// canUseTool reports every permission request (tool approvals, AskUserQuestion,
// ExitPlanMode) to the gateway and waits for the user's answer. Nothing is
// approved here on its own; a request whose turn is aborted is withdrawn.
function permissionHandler(sessionId: string, pending: Map<string, Pending>): CanUseTool {
  return (toolName, input, opts) =>
    new Promise<PermissionResult>((resolve) => {
      const requestId = opts.toolUseID;
      const done = (r: PermissionResult) => {
        if (pending.delete(requestId)) {
          emit(sessionId, "permissionResolved", { requestId });
        }
        resolve(r);
      };
      pending.set(requestId, { toolName, input, suggestions: opts.suggestions, resolve: done });
      opts.signal.addEventListener("abort", () => done({ behavior: "deny", message: "The request was cancelled." }));
      emit(sessionId, "permission", {
        requestId,
        toolName,
        input,
        title: opts.title,
        displayName: opts.displayName,
        description: opts.description,
        decisionReason: opts.decisionReason,
        blockedPath: opts.blockedPath,
        agentId: opts.agentID,
        canAlwaysAllow: !!opts.suggestions?.length && !opts.suppressAlwaysAllowRule,
      });
    });
}

function forward(s: Session, m: SDKMessage) {
  switch (m.type) {
    case "system":
      switch (m.subtype) {
        case "init":
          emit(s.id, "init", {
            model: m.model,
            permissionMode: m.permissionMode,
            apiKeySource: m.apiKeySource,
            claudeCodeVersion: m.claude_code_version,
            effort: m.effort ?? null,
            cwd: m.cwd,
          });
          return;
        case "session_state_changed":
          emit(s.id, "state", { state: m.state });
          return;
        case "status":
          if (m.permissionMode) emit(s.id, "mode", { permissionMode: m.permissionMode });
          return;
      }
      return;
    case "stream_event": {
      if (m.parent_tool_use_id) return; // subagents' text is not the reply
      const ev = m.event as Json & { type: string };
      if (ev.type === "message_start") {
        const msg = ev.message as { id?: string; model?: string };
        emit(s.id, "messageStart", { messageId: msg.id, model: msg.model });
      } else if (ev.type === "content_block_delta") {
        const d = ev.delta as { type: string; text?: string };
        if (d.type === "text_delta" && d.text) emit(s.id, "delta", { text: d.text });
      }
      return;
    }
    case "assistant":
      if (m.parent_tool_use_id) return;
      emit(s.id, "assistant", { messageId: m.message.id, model: m.message.model, error: m.error ?? null });
      return;
    case "result":
      emit(s.id, "result", {
        subtype: m.subtype,
        isError: m.is_error,
        errors: "errors" in m ? m.errors : undefined,
        numTurns: m.num_turns,
        ...(s.resident ? residentResultFields(m) : {}),
      });
      return;
    case "auth_status":
      emit(s.id, "auth", { error: m.error ?? null });
      return;
  }
}

async function pump(s: Session) {
  let error: string | null = null;
  try {
    for await (const m of s.q) forward(s, m);
  } catch (e) {
    error = e instanceof Error ? e.message : String(e);
  }
  s.closed = true;
  for (const [, p] of s.pending) p.resolve({ behavior: "deny", message: "The session ended." });
  s.pending.clear();
  sessions.delete(s.id);
  emit(s.id, "exit", { error });
}

function withTimeout<T>(p: Promise<T>, ms: number, what: string): Promise<T> {
  return Promise.race([
    p,
    new Promise<T>((_, reject) => setTimeout(() => reject(new Error(`${what} timed out`)), ms)),
  ]);
}

async function start(p: Json) {
  const id = String(p.sessionId ?? "");
  if (!/^[0-9a-f-]{36}$/i.test(id)) throw new Error("sessionId must be a UUID");
  if (sessions.has(id)) throw new Error("session is already running");
  const input = new InputQueue();
  const pending = new Map<string, Pending>();
  const env = (p.env ?? {}) as Record<string, string>;
  const resident = p.resident === true;
  const q = query({
    prompt: input,
    options: {
      cwd: String(p.cwd),
      // Resume keeps the id; a new session gets the id the gateway chose,
      // so the gateway can report it before the first reply.
      ...(p.resume ? { resume: id } : { sessionId: id }),
      ...(p.model ? { model: String(p.model) } : {}),
      ...(p.effort ? { effort: p.effort as never } : {}),
      // Unset, Claude Code starts in the mode the user's settings choose,
      // exactly as the CLI does.
      ...(p.permissionMode ? { permissionMode: p.permissionMode as PermissionMode } : {}),
      // The same configuration the CLI loads: user, project and local
      // settings (CLAUDE.md, .claude/, skills, hooks, MCP servers, agents,
      // permission rules) and Claude Code's own system prompt.
      settingSources: ["user", "project", "local"],
      systemPrompt: { type: "preset", preset: "claude_code" },
      // Sessions keep their transcript, the history the gateway reads.
      // A resident run keeps one only when the gateway asks (persist), so
      // that the user can continue it as a session; the gateway hides it
      // from the session list until then and removes it when not needed.
      persistSession: resident ? p.persist === true : true,
      includePartialMessages: !resident,
      pathToClaudeCodeExecutable: String(p.claudePath),
      env,
      ...(p.outputFormat ? { outputFormat: p.outputFormat as never } : {}),
      ...(Array.isArray(p.allowedTools) ? { allowedTools: p.allowedTools as string[] } : {}),
      ...(Array.isArray(p.disallowedTools) ? { disallowedTools: p.disallowedTools as string[] } : {}),
      canUseTool: resident ? residentPermissionHandler((event, data) => emit(id, event, data)) : permissionHandler(id, pending),
      stderr: (d: string) => log("claude stderr", { sessionId: id, text: d.slice(0, 500) }),
    },
  });
  const s: Session = { id, q, input, pending, closed: false, resident };
  sessions.set(id, s);
  void pump(s);
  let init;
  try {
    init = await withTimeout(q.initializationResult(), 90_000, "Claude Code startup");
  } catch (e) {
    s.closed = true;
    input.end();
    q.close();
    sessions.delete(id);
    throw e;
  }
  return {
    sessionId: id,
    // Not in the typed response, but reported by current CLIs.
    permissionMode: (init as unknown as Json).current_permission_mode ?? null,
    claudeCodeVersion: init.claude_code_version,
    account: {
      subscriptionType: init.account?.subscriptionType ?? null,
      apiKeySource: init.account?.apiKeySource ?? null,
      tokenSource: init.account?.tokenSource ?? null,
      apiProvider: init.account?.apiProvider ?? null,
    },
    models: (init.models ?? []).map((m) => ({
      value: m.value,
      displayName: m.displayName,
      description: m.description,
      efforts: m.supportedEffortLevels ?? [],
      // The gateway offers auto mode on plan approval for these models.
      resolvedModel: m.resolvedModel ?? null,
      supportsAutoMode: !!m.supportsAutoMode,
    })),
  };
}

function respond(p: Json) {
  const s = session(p.sessionId);
  const req = s.pending.get(String(p.requestId));
  if (!req) throw new Error("interaction is no longer pending");
  if (p.behavior === "allow") {
    const result: PermissionResult = { behavior: "allow" };
    if (p.updatedInput) result.updatedInput = p.updatedInput as Record<string, unknown>;
    // "For this session" keeps the rule in the session only, never in the
    // user's settings files.
    if (p.alwaysAllow && req.suggestions?.length) {
      result.updatedPermissions = req.suggestions.map((u) => ({ ...u, destination: "session" }) as PermissionUpdate);
    }
    req.resolve(result);
  } else {
    req.resolve({ behavior: "deny", message: String(p.message || "The user denied this."), ...(p.interrupt ? { interrupt: true } : {}) });
  }
  return {};
}

async function handle(method: string, p: Json): Promise<unknown> {
  switch (method) {
    case "hello":
      return { version: VERSION };
    case "start":
      return start(p);
    case "send": {
      const s = session(p.sessionId);
      s.input.push(userMessage(String(p.text ?? ""), (p.images as string[]) ?? []));
      return {};
    }
    case "interrupt":
      await session(p.sessionId).q.interrupt();
      return {};
    case "setModel":
      // Applies from the next model request of this session, history kept.
      await session(p.sessionId).q.setModel(p.model ? String(p.model) : undefined);
      return {};
    case "setEffort":
      await session(p.sessionId).q.applyFlagSettings({ effortLevel: (p.effort as never) ?? null });
      return {};
    case "setMode":
      await session(p.sessionId).q.setPermissionMode(p.mode as PermissionMode);
      return {};
    case "respond":
      return respond(p);
    case "stop": {
      const s = sessions.get(String(p.sessionId));
      if (s && !s.closed) {
        s.input.end();
        s.q.close();
      }
      return {};
    }
    default:
      throw new Error("unknown method " + method);
  }
}

function closeAll() {
  for (const s of sessions.values()) {
    if (s.closed) continue;
    s.input.end();
    s.q.close();
  }
}

process.on("SIGTERM", () => {
  closeAll();
  process.exit(0);
});

const rl = createInterface({ input: process.stdin });
rl.on("line", async (line) => {
  let req: { id?: number; method?: string; params?: Json };
  try {
    req = JSON.parse(line);
  } catch {
    log("invalid request line");
    return;
  }
  try {
    const result = await handle(String(req.method), req.params ?? {});
    write({ id: req.id, result: result ?? {} });
  } catch (e) {
    write({ id: req.id, error: { message: e instanceof Error ? e.message : String(e) } });
  }
});
rl.on("close", () => {
  // The gateway is gone: end every Claude Code process with it.
  closeAll();
  setTimeout(() => process.exit(0), 2000).unref();
});
