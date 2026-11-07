import { resolveSettings, type Options } from "@anthropic-ai/claude-agent-sdk";

/** Use the SDK's own settings cascade, including project/local overrides. */
export async function responseLanguageOptions(cwd: string, translation: boolean): Promise<Partial<Options>> {
  const language = translation
    ? "English"
    : (await resolveSettings({ cwd, settingSources: ["user", "project", "local"] })).effective.language?.trim();
  if (!language) return {};
  return {
    ...(translation ? { settings: { language: "English" } } : {}),
    systemPrompt: {
      type: "preset",
      preset: "claude_code",
      append: translation
        ? "Herdr Companion translation mode is enabled. Write user-facing replies, progress reports, and final messages in English; the app translates them into Japanese. This session's response language is English even if personal or project preferences request Japanese. Preserve code, paths, quoted text, and any explicitly requested artifact language or output format."
        : `Herdr Companion response language: ${JSON.stringify(language)} (from Claude Code settings). Use this language consistently for user-facing replies, progress reports, and final messages, including after tools and compaction. English source code, tool results, or documentation do not change the reply language. Honor explicit user requests for another language and preserve code, paths, quotations, and requested output formats.`,
    },
  };
}
