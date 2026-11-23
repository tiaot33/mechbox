// 组安装、加入组的逐个结果：跳过的成员用中文说明原因，路径按界面惯例缩写，不显示技能库的英文原始错误。
import { describe, expect, it } from "vitest";
import { AgentCode, ErrorCode, GroupMemberOutcome, ScopeKind, type GroupMemberResult } from "@bindings/internal/library";
import type { Snapshot } from "./library";
import { memberRows } from "./result-rows";

const s = {
  info: { home_dir: "/home" },
  skills: [
    { id: 1, name: "alpha", source: { kind: "" } },
    { id: 2, name: "alpha", source: { kind: "" } },
  ],
  agents: [{ code: "codex", name: "Codex" }],
  skillScopes: [],
  projects: [{ id: 7, name: "web", path: "/home/code/web" }],
} as unknown as Snapshot;
const skipped = (error: GroupMemberResult["error"], skill_id = 1): GroupMemberResult => ({
  skill_id,
  scope: { kind: ScopeKind.ScopeGlobal, agent: AgentCode.AgentCodex },
  outcome: GroupMemberOutcome.MemberSkipped,
  error,
});

describe("memberRows", () => {
  it("这个技能的另一个作用域已装在同一个文件夹(目录经符号链接是同一处)时说是它自己，不当成项目不在这台机器上", () => {
    const [row] = memberRows(s, [skipped({ code: ErrorCode.FailedPrecondition, message: "another scope", details: { skill_id: 1, name: "alpha", path: "/home/.claude/skills/alpha" } })]);
    expect(row.reason).toBe("无法安装：这个技能的另一个作用域已装在\n~/.claude/skills/alpha\n先卸载它，再安装");
    expect(row.occupier).toBeUndefined();
  });
  it("只带路径、认不出原因时照原样写，不猜成项目不在这台机器上", () => {
    const [row] = memberRows(s, [skipped({ code: ErrorCode.FailedPrecondition, message: "something else", details: { path: "/home/.claude/skills" } })]);
    expect(row.reason).not.toContain("不在这台机器上");
  });
  it("目标处有不是库装的同名文件夹：中文说明没有覆盖，路径缩写成 ~,不带英文原始错误", () => {
    const [row] = memberRows(s, [skipped({ code: ErrorCode.AlreadyExists, message: "install group: target already exists", details: { path: "/home/.agents/skills/alpha" } })]);
    expect(row.reason).toBe("~/.agents/skills/alpha 已存在，但不是从技能库装的，没有覆盖。要覆盖，再组安装一次并选「覆盖这些文件夹」");
    expect(row.reason).not.toContain("target already exists");
  });
  it("被另一个技能占用、组里同名、项目不在这台机器上，各按原因说", () => {
    const occupied = memberRows(s, [skipped({ code: ErrorCode.FailedPrecondition, message: "install group: occupied", details: { skill_id: 2, name: "alpha", path: "/home/.agents/skills/alpha" } })])[0];
    expect(occupied.reason).toContain("~/.agents/skills/alpha");
    expect(occupied.reason).toMatch(/^无法安装/);
    expect(occupied.occupier).toBe(2);
    const duplicate = memberRows(s, [skipped({ code: ErrorCode.FailedPrecondition, message: "install group: duplicate", details: { name: "alpha", path: "/home/.agents/skills/alpha" } })])[0];
    expect(duplicate.reason).toBe("组里有几个技能都叫「alpha」，会装进同一个文件夹 ~/.agents/skills/alpha，这几个都没装");
    const added = memberRows(s, [skipped({ code: ErrorCode.FailedPrecondition, message: "add to group: duplicate", details: { name: "alpha", path: "/home/.agents/skills/alpha" } })], true)[0];
    expect(added.reason).toBe("组里已有同名的技能「alpha」，会装进同一个文件夹 ~/.agents/skills/alpha，这个没装");
    const missing = memberRows(s, [skipped({ code: ErrorCode.FailedPrecondition, message: "root missing", details: { path: "/home/code/web", project_id: 7 } })])[0];
    expect(missing.reason).toBe("~/code/web 不在这台机器上，装不上；先在右栏改项目位置指到本机");
  });
});
