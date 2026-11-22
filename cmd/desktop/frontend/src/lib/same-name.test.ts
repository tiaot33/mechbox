// 同名：同一个 Agent 的全局和项目里同名时的后果，同一层的别处同名不同内容的几份。
import { describe, expect, it } from "vitest";
import {
  SameNamePairKind,
  SameNameRule,
  ScopeKind,
  type Agent,
  type PlannedUnmanagedSkill,
  type SameNameFolder,
  type SameNameSkill,
  type UnmanagedSkill,
} from "@bindings/internal/library";
import type { Snapshot } from "./library";
import { sameNameCopies, sameNameNote, sameNameScopes, skillSameNameNote } from "./same-name";

const snapshot = (s: Partial<Snapshot>) =>
  ({ collections: [], groups: [], skills: [], skillScopes: [], unmanaged: [], agents: [], projects: [], info: { home_dir: "/home" }, ...s }) as unknown as Snapshot;
const agent = (code: string, projectDir: string) => ({ code, name: code, project_directories: [projectDir], detected: true }) as Agent;
const where = (agent: string, project_id?: number) =>
  project_id ? { kind: ScopeKind.ScopeProject, agent, project_id } : { kind: ScopeKind.ScopeGlobal, agent };
// globalAndProject 是同一个 Agent 的全局和一个项目里同名的一对:first 是全局的,second 是项目里的。
const globalAndProject = (agent: string, project_id: number, name: string, global: Partial<SameNameFolder>, local: Partial<SameNameFolder>): SameNameSkill => ({
  kind: SameNamePairKind.SameNameGlobalAndProject,
  name,
  first: { scope: where(agent, undefined), directory: "", folder: "", ...global } as SameNameFolder,
  second: { scope: where(agent, project_id), directory: "", folder: "", ...local } as SameNameFolder,
});
const unmanaged = (name: string, agent: string, project_id?: number) => ({ name, folder: name, scope: where(agent, project_id) }) as UnmanagedSkill;

describe("sameNameNote", () => {
  it("全局那一份是和库里一样、不是 app 装的副本时，不说不在技能库里", () => {
    const pair = globalAndProject("claude-code", 1, "x", { folder: "/home/.claude/skills/x" }, { folder: "/home/proj/.claude/skills/x" });
    const base = {
      agents: [{ ...agent("claude-code", ".claude/skills"), same_name: SameNameRule.SameNameUnknown }],
      projects: [{ id: 1, name: "proj", path: "/home/proj" }] as Snapshot["projects"],
      sameName: [pair],
    };
    const copy = { ...unmanaged("x", "claude-code"), folder: pair.first.folder, skill_id: 3, version_id: 1 };
    expect(sameNameNote(snapshot({ ...base, unmanaged: [copy] } as Partial<Snapshot>), "claude-code@1", pair.second.folder)?.text).toBe(
      "全局也有同名的 x（和库里的一样、不是 app 装的）",
    );
    expect(sameNameNote(snapshot({ ...base, unmanaged: [{ ...copy, skill_id: undefined }] } as Partial<Snapshot>), "claude-code@1", pair.second.folder)?.text).toBe(
      "全局也有同名的 x（不在技能库里）",
    );
  });
});

describe("skillSameNameNote", () => {
  it("库里的技能在全局和项目里各装一份时，这一对只说一次：哪两处、后果", () => {
    const pair = globalAndProject("codex", 1, "docs", { folder: "/home/.codex/skills/docs", skill_id: 4 }, { folder: "/home/web/.agents/skills/docs", skill_id: 4 });
    const s = snapshot({
      agents: [{ ...agent("codex", ".agents/skills"), name: "Codex", same_name: SameNameRule.SameNameBothShown }],
      projects: [{ id: 1, name: "web-app", path: "/home/web" }] as Snapshot["projects"],
      sameName: [pair, pair],
    } as Partial<Snapshot>);
    const note = skillSameNameNote(s, 4);
    expect(note?.text).toBe("Codex 的全局（库装的）和项目「web-app」（库装的）里都有 docs：Codex 在「web-app」里两份都会出现");
    expect(note?.title).toBe(note?.text);
    expect(skillSameNameNote(s, 5)).toBeUndefined();
  });
});

describe("同名不同内容的几份", () => {
  const u = (name: string, agent: string, folder: string, project_id?: number) => ({ name, folder, scope: where(agent, project_id) }) as UnmanagedSkill;
  const here = u("notes", "claude-code", "/home/.claude/skills/notes");
  const there = u("notes", "codex", "/home/.agents/skills/notes");

  it("和同一层的别处比：全局比各 Agent 的全局，项目里的不算", () => {
    const s = snapshot({ unmanaged: [here, there, u("notes", "codex", "/p/.agents/skills/notes", 2), u("other", "codex", "/home/.agents/skills/other")] });
    expect(sameNameScopes(s, "claude-code@0")).toEqual([where("claude-code"), where("codex")]);
  });

  it("别处是同一个文件夹(符号链接指过来的)时不用比", () => {
    const linked = { ...there, link_target: here.folder } as UnmanagedSkill;
    expect(sameNameScopes(snapshot({ unmanaged: [here, linked] }), "claude-code@0")).toEqual([]);
  });

  it("技能库比出内容不同(conflict)时，按这里的文件夹列出别处的那几份;内容一样的合成一条，不算", () => {
    const plan = [
      { name: "notes", conflict: true, folders: [here] },
      { name: "notes", conflict: true, folders: [there] },
      { name: "same", folders: [u("same", "claude-code", "/a"), u("same", "codex", "/b")] },
    ] as PlannedUnmanagedSkill[];
    const copies = sameNameCopies(plan, "claude-code@0");
    expect([...copies.keys()]).toEqual([here.folder]);
    expect(copies.get(here.folder)).toEqual([there]);
  });

  it("同一个作用域里内容一样的另一个文件夹也标 conflict,但不算内容不同", () => {
    // Claude Code 全局有 pdf 和 pdf-copy,Codex 全局有一份 pdf,三份一样：计划拆成两条(一处只能记一个安装)。
    const pdf = u("pdf", "claude-code", "/home/.claude/skills/pdf");
    const copy = u("pdf", "claude-code", "/home/.claude/skills/pdf-copy");
    const codex = u("pdf", "codex", "/home/.agents/skills/pdf");
    const plan = [
      { name: "pdf", conflict: true, folders: [pdf, codex] },
      { name: "pdf", conflict: true, folders: [copy] },
    ] as PlannedUnmanagedSkill[];
    expect(sameNameCopies(plan, "codex@0").size).toBe(0);
  });
});
