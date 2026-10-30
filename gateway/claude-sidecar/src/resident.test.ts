import { describe, expect, test } from "bun:test";
import { residentPermissionHandler, residentResultFields, summarizeInput } from "./resident";

const opts = (id: string) =>
  ({ toolUseID: id, signal: new AbortController().signal, suggestions: [] }) as unknown as Parameters<
    ReturnType<typeof residentPermissionHandler>
  >[2];

describe("常駐実行の権限処理", () => {
  test("許可されていないツールは承認せず拒否して報告する", async () => {
    const events: [string, Record<string, unknown>][] = [];
    const handler = residentPermissionHandler((e, d) => events.push([e, d]));
    const r = await handler("Bash", { command: "rm -rf build" }, opts("tool-1"));
    expect(r.behavior).toBe("deny");
    expect(events).toHaveLength(1);
    expect(events[0][0]).toBe("denied");
    expect(events[0][1]).toMatchObject({ requestId: "tool-1", toolName: "Bash", input: "rm -rf build" });
  });

  test("ユーザーへの質問ツールは結果のquestionで返すよう伝えて拒否する", async () => {
    const handler = residentPermissionHandler(() => {});
    const r = await handler("AskUserQuestion", { questions: [] }, opts("tool-2"));
    expect(r.behavior).toBe("deny");
    expect(r.behavior === "deny" && r.message).toContain("blocked");
  });

  test("入力の要約はファイル内容を含めずパスやコマンドだけにする", () => {
    expect(summarizeInput({ file_path: "/tmp/a.txt", content: "secret" })).toBe("/tmp/a.txt");
    expect(summarizeInput({ content: "secret" })).toBe("");
  });
});

describe("常駐実行の結果", () => {
  test("構造化出力と使用量と権限拒否を結果に含める", () => {
    const m = {
      type: "result",
      subtype: "success",
      is_error: false,
      result: "{}",
      structured_output: { outcome: "unchanged" },
      usage: { input_tokens: 3, output_tokens: 4 },
      modelUsage: {},
      total_cost_usd: 0.01,
      duration_ms: 1200,
      num_turns: 1,
      permission_denials: [{ tool_name: "Bash", tool_use_id: "x", tool_input: {} }],
    } as never;
    expect(residentResultFields(m)).toMatchObject({
      structuredOutput: { outcome: "unchanged" },
      totalCostUsd: 0.01,
      durationMs: 1200,
      permissionDenials: ["Bash"],
    });
  });
});
