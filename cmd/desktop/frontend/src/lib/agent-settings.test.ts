import { describe, expect, it } from "vitest";
import { AgentCode, type AgentSettings } from "@bindings/internal/library";
import { invocationNote, permissionNote, settingSections } from "./agent-settings";

const claude = AgentCode.AgentClaudeCode;
const codex = AgentCode.AgentCodex;

const settings: AgentSettings = {
  claude_code: {
    model_invocation_disabled: true,
    allowed_tools: ["Bash(git push *)", "Read"],
    hooks: [{ event: "PreToolUse", matcher: "Bash", type: "command", detail: "./check.sh" }],
    fork: true,
    agent: "Explore",
  },
  codex: { products: ["codex"], tools: [{ type: "mcp", value: "github", url: "https://example.com/mcp" }] },
};

describe("invocationNote", () => {
  it("只看读这个作用域的 Agent", () => {
    expect(invocationNote(settings, [claude], "deploy")?.text).toBe("只能手动调用");
    // Codex 没关掉自动调用：默认，不写。
    expect(invocationNote(settings, [codex], "deploy")).toBeUndefined();
    expect(invocationNote(settings, ["custom-1" as AgentCode], "deploy")).toBeUndefined();
  });

  it("几个 Agent 说法一样只写一次，悬停分别说明", () => {
    const both: AgentSettings = { claude_code: { model_invocation_disabled: true }, codex: { implicit_invocation_disabled: true } };
    const note = invocationNote(both, [claude, codex], "deploy");
    expect(note?.text).toBe("只能手动调用");
    expect(note?.title).toContain("/deploy");
    expect(note?.title).toContain("$deploy");
  });

  it("只能自动调用、都关掉", () => {
    expect(invocationNote({ claude_code: { user_invocation_disabled: true } }, [claude], "x")?.text).toBe("只能自动调用");
    expect(invocationNote({ claude_code: { user_invocation_disabled: true, model_invocation_disabled: true } }, [claude], "x")?.text).toBe("调用不了");
  });
});

describe("settingSections", () => {
  it("逐项写出，从作用域打开时那个 Agent 在前", () => {
    const sections = settingSections(settings, "deploy");
    expect(sections.map((s) => s.agent)).toEqual([claude, codex]);
    expect(sections[0].rows.map((r) => r.key)).toEqual(["invocation", "allowed", "hooks", "run"]);
    expect(sections[0].rows.find((r) => r.key === "hooks")?.values).toEqual(["PreToolUse (Bash): ./check.sh"]);
    expect(sections[1].rows.find((r) => r.key === "tools")?.values).toEqual(["github (mcp): https://example.com/mcp"]);
    expect(settingSections(settings, "deploy", codex).map((s) => s.agent)).toEqual([codex, claude]);
  });

  it("没有设置时没有任何一节", () => {
    expect(settingSections(undefined, "deploy")).toEqual([]);
  });
});

describe("permissionNote", () => {
  it("写出各有几个，悬停逐项列出", () => {
    const note = permissionNote(settings);
    expect(note?.text).toBe("2 个工具不用确认 · 1 个 hook · 接入 1 个工具");
    expect(note?.title).toContain("Bash(git push *)");
    expect(note?.title).toContain("github (mcp): https://example.com/mcp");
  });

  it("只改调用方式、运行方式的没有权限可说", () => {
    expect(permissionNote({ claude_code: { model_invocation_disabled: true, model: "opus" } })).toBeUndefined();
  });
});
