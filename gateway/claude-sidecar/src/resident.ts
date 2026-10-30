// Helpers for resident runs: unattended, one-shot queries the gateway starts
// for its resident tasks (常駐). They never wait for a user.

import type { CanUseTool, SDKResultMessage } from "@anthropic-ai/claude-agent-sdk";

type Json = Record<string, unknown>;

// residentPermissionHandler denies everything the run's permission rules did
// not already allow: nobody is there to answer. Each denial is reported so
// the gateway can tell the user which permission was missing.
export function residentPermissionHandler(report: (event: string, data: Json) => void): CanUseTool {
  return async (toolName, input, opts) => {
    report("denied", {
      requestId: opts.toolUseID,
      toolName,
      title: opts.title ?? null,
      description: opts.description ?? null,
      blockedPath: opts.blockedPath ?? null,
      input: summarizeInput(input),
    });
    const why =
      toolName === "AskUserQuestion"
        ? 'No user is present in this unattended run. Finish with outcome "blocked" and put the question in the result instead.'
        : `${toolName} is not permitted for this unattended task. If the work needs it, finish with outcome "blocked" and say which permission is missing.`;
    return { behavior: "deny", message: why };
  };
}

// summarizeInput keeps a denial report short: command lines and paths, not
// file contents.
export function summarizeInput(input: Record<string, unknown>): string {
  for (const k of ["command", "file_path", "path", "url", "pattern", "query"]) {
    const v = input[k];
    if (typeof v === "string") return v.slice(0, 300);
  }
  return "";
}

// residentResultFields is what a resident run reports with its result: the
// final text, the structured output (outputFormat), usage and the tool
// calls its permissions denied.
export function residentResultFields(m: SDKResultMessage): Json {
  return {
    result: "result" in m ? m.result : null,
    structuredOutput: "structured_output" in m ? (m.structured_output ?? null) : null,
    usage: m.usage ?? null,
    modelUsage: m.modelUsage ?? null,
    totalCostUsd: m.total_cost_usd ?? 0,
    durationMs: m.duration_ms ?? 0,
    terminalReason: m.terminal_reason ?? null,
    permissionDenials: (m.permission_denials ?? []).map((d) => d.tool_name),
  };
}
