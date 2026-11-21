// 一次确认入库未纳管技能：最终用的来源，确认框里怎么分组，勾上的里要另外提醒的，确认后怎么分批。
import { describe, expect, it } from "vitest";
import { AgentCode, ScopeKind, type PlannedUnmanagedSkill, type UnmanagedSkill } from "@bindings/internal/library";
import { fetchBatches, fetchGroups, identicalGroup, itemOf, keyOf, noSource, pickedNotes, sourceKey, sourceOf } from "./fetch-unmanaged-rules";

const scope = { kind: ScopeKind.ScopeGlobal, agent: AgentCode.AgentClaudeCode };
const folder = (name: string) => ({ scope, name, folder: `/home/.claude/skills/${name}` }) as UnmanagedSkill;
const planned = (name: string, extra: Partial<PlannedUnmanagedSkill> = {}) => ({ name, folders: [folder(name)], ...extra }) as PlannedUnmanagedSkill;

describe("一条最终用的来源", () => {
  const p = planned("a", { source_input: "owner/repo", source_ref: "dev" });

  it("没改过链接时用来源记录认出的来源和分支", () => {
    expect(sourceOf(p)).toEqual({ input: "owner/repo", ref: "dev" });
  });

  it("改过链接时用改过的;还是认出的那个来源时分支跟着走，换了来源时不带分支;清空就是没有来源", () => {
    expect(sourceOf(p, " owner/repo ")).toEqual({ input: "owner/repo", ref: "dev" });
    expect(sourceOf(p, "other/repo")).toEqual({ input: "other/repo", ref: undefined });
    expect(sourceOf(p, "  ")).toEqual({ input: undefined, ref: undefined });
  });

  it("入库请求带上各处文件夹和最终用的来源;一条的身份是第一处文件夹", () => {
    expect(itemOf(p, "other/repo")).toEqual({ folders: [{ scope, name: "a" }], source_input: "other/repo", source_ref: undefined });
    expect(keyOf(p)).toBe("/home/.claude/skills/a");
    expect(keyOf(planned("b", { folders: [] }))).toBe("b");
  });
});

describe("确认框里怎么分组", () => {
  const plan = [
    planned("none"),
    planned("lib", { skill_id: 3, source_input: "owner/repo" }),
    planned("b", { source_input: "z/repo" }),
    planned("bad", { error: { code: "invalid_argument", message: "x" } as PlannedUnmanagedSkill["error"] }),
    planned("dup", { conflict: true, source_input: "a/repo" }),
    planned("a", { source_input: "a/repo", source_ref: "dev" }),
    planned("dup", { conflict: true }),
  ];

  it("不能入库的单列;同名不同内容的按名字单列;其余按来源分组，有来源的在前、和库里一样的其次、没有来源的在最后", () => {
    const r = fetchGroups(plan);
    expect(r.usable).toEqual([0, 1, 2, 4, 5, 6]);
    expect(r.unusable.map((p) => p.name)).toEqual(["bad"]);
    expect([...r.conflicts]).toEqual([["dup", [4, 6]]]);
    expect(r.ordered).toEqual([
      [sourceKey("a/repo", "dev"), [5]],
      [sourceKey("z/repo"), [2]],
      [identicalGroup, [1]],
      [noSource, [0]],
    ]);
  });

  it("勾上的里归项目仓库管的、指向 Agent 目录以外的另外提醒，按计划里的先后", () => {
    const notes = pickedNotes([planned("a", { in_repository: true }), planned("b", { link_outside: true }), planned("c", { in_project_lock: true })], new Set([2, 0, 1]));
    expect(notes.chosen).toEqual([0, 1, 2]);
    expect(notes.team.map((p) => p.name)).toEqual(["a", "c"]);
    expect(notes.outside.map((p) => p.name)).toEqual(["b"]);
  });
});

describe("确认后按来源分批", () => {
  it("同一个来源一批，按最终用的来源算(改过的链接算改过的);没有来源的一批在最后", () => {
    const items = [
      { planned: planned("a") },
      { planned: planned("b", { source_input: "x/repo" }) },
      { planned: planned("c", { source_input: "y/repo" }), link: "x/repo" },
      { planned: planned("d", { source_input: "y/repo" }) },
    ];
    expect(fetchBatches(items).map(([key, members]) => [key, members.map((m) => m.planned.name)])).toEqual([
      [sourceKey("x/repo"), ["b", "c"]],
      [sourceKey("y/repo"), ["d"]],
      [noSource, ["a"]],
    ]);
  });
});
