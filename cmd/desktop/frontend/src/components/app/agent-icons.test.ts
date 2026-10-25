import ClaudeCode from "@lobehub/icons/es/ClaudeCode/components/Color";
import GeminiCLI from "@lobehub/icons/es/GeminiCLI/components/Color";
import Kiro from "@lobehub/icons/es/Kiro/components/Color";
import { describe, expect, test } from "vitest";
import aider from "./agent-logos/aider.svg";
import anthropic from "./agent-logos/anthropic.svg";
import augmentCode from "./agent-logos/augment-code.svg";
import blackbox from "./agent-logos/blackbox.svg";
import bolt from "./agent-logos/bolt.svg";
import claude from "./agent-logos/claude.svg";
import codex from "./agent-logos/codex.svg";
import comet from "./agent-logos/comet.svg";
import deepseek from "./agent-logos/deepseek.svg";
import factory from "./agent-logos/factory.svg";
import gemini from "./agent-logos/gemini.svg";
import genspark from "./agent-logos/genspark.svg";
import githubCopilot from "./agent-logos/github-copilot.svg";
import grok from "./agent-logos/grok.svg";
import huggingFace from "./agent-logos/hugging-face.svg";
import kiloCode from "./agent-logos/kilo-code.svg";
import lmStudio from "./agent-logos/lm-studio.svg";
import lovable from "./agent-logos/lovable.svg";
import mcp from "./agent-logos/mcp.svg";
import minimax from "./agent-logos/minimax.svg";
import mistral from "./agent-logos/mistral.svg";
import muse from "./agent-logos/muse.svg";
import ollama from "./agent-logos/ollama.svg";
import openrouter from "./agent-logos/openrouter.svg";
import perplexity from "./agent-logos/perplexity.svg";
import pi from "./agent-logos/pi.svg";
import qodo from "./agent-logos/qodo.svg";
import v0 from "./agent-logos/v0.svg";
import warp from "./agent-logos/warp.svg";
import windsurf from "./agent-logos/windsurf.svg";
import xai from "./agent-logos/xai.svg";
import zAI from "./agent-logos/z-ai.svg";
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
    ["Droid", factory],
    ["Augment Code", augmentCode],
    ["Auggie CLI", augmentCode],
    ["Warp", warp],
    ["Grok", grok],
    ["Grok CLI", grok],
    ["xAI", xai],
    ["xAI Grok", grok],
    ["Anthropic", anthropic],
    ["Mistral Vibe", mistral],
    ["DeepSeek", deepseek],
    ["Z.ai", zAI],
    ["GLM Coding", zAI],
    ["MiniMax Agent", minimax],
    ["Perplexity", perplexity],
    ["Perplexity Comet", comet],
    ["Blackbox AI", blackbox],
    ["Lovable", lovable],
    ["v0", v0],
    ["Bolt.new", bolt],
    ["Genspark", genspark],
    ["Muse", muse],
    ["Ollama", ollama],
    ["LM Studio", lmStudio],
    ["OpenRouter", openrouter],
    ["Hugging Face", huggingFace],
    ["MCP", mcp],
  ])("%s", (name, expected) => {
    expect(icon(name)).toBe(expected);
  });

  test("认不出", () => {
    expect(agentBrand("公司内部助手")).toBeUndefined();
    expect(agentBrand("  ")).toBeUndefined();
  });

  test("短关键词与常见单词只认整个词", () => {
    for (const name of ["API Helper", "Android Studio", "Optimized", "Augmented Coder", "Warpgate Helper", "Agent Factory", "Raider", "Continuer", "Amuse", "Comets", "Boltzmann", "MCPHub"]) {
      expect(agentBrand(name), name).toBeUndefined();
    }
  });
});
