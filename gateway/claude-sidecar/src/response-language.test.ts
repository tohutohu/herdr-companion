import { expect, test } from "bun:test";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { responseLanguageOptions } from "./response-language";

test("通常時はローカル設定の回答言語を補強し翻訳時だけ英語で上書きする", async () => {
  const cwd = mkdtempSync(join(tmpdir(), "herdr-language-"));
  try {
    mkdirSync(join(cwd, ".claude"));
    writeFileSync(join(cwd, ".claude/settings.json"), JSON.stringify({ language: "French" }));
    writeFileSync(join(cwd, ".claude/settings.local.json"), JSON.stringify({ language: "日本語" }));
    const normal = await responseLanguageOptions(cwd, false);
    expect(normal.settings).toBeUndefined();
    expect(normal.systemPrompt).toMatchObject({ preset: "claude_code", append: expect.stringContaining("日本語") });
    const translated = await responseLanguageOptions(cwd, true);
    expect(translated.settings).toEqual({ language: "English" });
    expect(translated.systemPrompt).toMatchObject({ append: expect.stringContaining("translation mode is enabled") });
    // Switching back must not persist an English override to any settings file.
    expect(await responseLanguageOptions(cwd, false)).toEqual(normal);
  } finally {
    rmSync(cwd, { recursive: true, force: true });
  }
});
