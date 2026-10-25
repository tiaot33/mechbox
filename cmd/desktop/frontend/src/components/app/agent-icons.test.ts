import Claude from "@lobehub/icons/es/Claude/components/Color";
import ClaudeCode from "@lobehub/icons/es/ClaudeCode/components/Color";
import Codex from "@lobehub/icons/es/Codex/components/Color";
import GeminiCLI from "@lobehub/icons/es/GeminiCLI/components/Color";
import GithubCopilot from "@lobehub/icons/es/GithubCopilot/components/Mono";
import { describe, expect, test } from "vitest";
import { agentBrand } from "./agent-icons";

describe("agentBrand", () => {
  test.each([
    ["Claude Code", ClaudeCode],
    ["claude_code", ClaudeCode],
    ["Claude", Claude],
    ["Codex", Codex],
    ["Gemini CLI", GeminiCLI],
    ["GitHub Copilot", GithubCopilot],
    ["My GitHub Agent", GithubCopilot],
  ])("%s", (name, icon) => {
    expect(agentBrand(name)?.Icon).toBe(icon);
  });

  test("认不出", () => {
    expect(agentBrand("公司内部助手")).toBeUndefined();
    expect(agentBrand("  ")).toBeUndefined();
  });
});
