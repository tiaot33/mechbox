import ClaudeCode from "@lobehub/icons/es/ClaudeCode/components/Color";
import GeminiCLI from "@lobehub/icons/es/GeminiCLI/components/Color";
import Kiro from "@lobehub/icons/es/Kiro/components/Color";
import { describe, expect, test } from "vitest";
import aider from "./agent-logos/aider.svg";
import claude from "./agent-logos/claude.svg";
import codex from "./agent-logos/codex.svg";
import factory from "./agent-logos/factory.svg";
import gemini from "./agent-logos/gemini.svg";
import githubCopilot from "./agent-logos/github-copilot.svg";
import kiloCode from "./agent-logos/kilo-code.svg";
import pi from "./agent-logos/pi.svg";
import qodo from "./agent-logos/qodo.svg";
import windsurf from "./agent-logos/windsurf.svg";
import zed from "./agent-logos/zed.svg";
import { agentBrand } from "./agent-icons";

function icon(name: string) {
  const brand = agentBrand(name);
  return brand && ("src" in brand ? brand.src : brand.Icon);
}

describe("agentBrand", () => {
  test.each([
    ["Claude Code", ClaudeCode],
    ["claude_code", ClaudeCode],
    ["Claude", claude],
    ["Codex", codex],
    ["Gemini CLI", GeminiCLI],
    ["Gemini", gemini],
    ["GitHub Copilot", githubCopilot],
    ["My GitHub Agent", githubCopilot],
    ["Kiro", Kiro],
    ["Kiro CLI", Kiro],
    ["Kilo Code", kiloCode],
    ["Devin Desktop", windsurf],
    ["Aider", aider],
    ["Qodo", qodo],
    ["Zed", zed],
    ["Pi", pi],
    ["pi coding agent", pi],
    ["Factory Droid", factory],
  ])("%s", (name, expected) => {
    expect(icon(name)).toBe(expected);
  });

  test("认不出", () => {
    expect(agentBrand("公司内部助手")).toBeUndefined();
    expect(agentBrand("  ")).toBeUndefined();
  });

  test("短关键词只认整个词", () => {
    expect(agentBrand("API Helper")).toBeUndefined();
    expect(agentBrand("Android Studio")).toBeUndefined();
    expect(agentBrand("Optimized")).toBeUndefined();
  });
});
