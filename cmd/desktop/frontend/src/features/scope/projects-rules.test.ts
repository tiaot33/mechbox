// 注销项目：选同时删除时会删掉的文件夹与旧链接。
import { describe, expect, it } from "vitest";
import { ScopeKind, type SkillScopeInspection } from "@bindings/internal/library";
import type { Snapshot } from "@/lib/library";
import { unregisterDeletes } from "./projects-rules";

describe("unregisterDeletes", () => {
  it("写出项目里会删的各处，以及技能库一并删掉的旧链接;别的项目的不算", () => {
    const at = (project_id: number, stale_links: string[] = []) =>
      ({ skill_id: 1, scope: { kind: ScopeKind.ScopeProject, agent: "codex", project_id }, folder: `/p${project_id}/.agents/skills/foo`, stale_links }) as SkillScopeInspection;
    const s = { skillScopes: [at(7, ["/p7/.claude/skills/foo"]), at(8, ["/p8/.claude/skills/foo"])] } as unknown as Snapshot;
    const r = unregisterDeletes(s, 7);
    expect(r.places.map((p) => p.folder)).toEqual(["/p7/.agents/skills/foo"]);
    expect(r.links).toEqual(["/p7/.claude/skills/foo"]);
  });
});
