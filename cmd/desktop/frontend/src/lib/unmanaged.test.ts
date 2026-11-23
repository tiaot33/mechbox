// 未纳管技能：归谁管、指向哪里的标注，SKILL.md 哪里写错了，和库里一样但不是 app 装的那一份怎么说。
import { describe, expect, it } from "vitest";
import { ScopeKind, SkillFileProblemKind, SkillScopeStatus, type Skill, type SkillScopeInspection, type UnmanagedSkill } from "@bindings/internal/library";
import type { Snapshot } from "./library";
import { extraCopy, fetchable, folderNotes, repoMark, handOverNote, identicalsOf, identicalState, skillFileProblemText } from "./unmanaged";

const where = (agent: string, project_id?: number) =>
  project_id ? { kind: ScopeKind.ScopeProject, agent, project_id } : { kind: ScopeKind.ScopeGlobal, agent };
const snapshot = (s: Partial<Snapshot>) => ({ agents: [], projects: [], skills: [], unmanaged: [], info: { home_dir: "/home" }, ...s }) as unknown as Snapshot;
const unmanaged = (name: string, agent: string, project_id?: number) => ({ name, folder: name, scope: where(agent, project_id) }) as UnmanagedSkill;

describe("folderNotes", () => {
  it("写出归项目仓库管的、指向 Agent 目录以外的;副本和不在技能库里的一行写同样的几条", () => {
    const plain = unmanaged("x", "claude-code");
    expect(folderNotes(plain)).toEqual([]);
    const notes = folderNotes({ ...plain, in_repository: true, in_project_lock: true, link_outside: true, link_target: "/home/code/x" });
    expect(notes.map((x) => x.text)).toEqual(["在项目仓库里", "链到 /home/code/x"]);
    expect(folderNotes({ ...plain, link_outside: true, link_target: "/home/me/code/x" }, "/home/me")[0].text).toBe("链到 ~/code/x");
    expect(notes[1].title).toContain("/home/code/x");
    expect(folderNotes({ ...plain, in_project_lock: true }).map((x) => x.text)).toEqual(["记在 skills-lock.json 里"]);
  });
});

describe("repoMark", () => {
  it("归项目仓库管的只标一条：提交在仓库里的优先，其次是记在 skills-lock.json 里", () => {
    expect(repoMark({ in_repository: true, in_project_lock: true })).toBe("repository");
    expect(repoMark({ in_project_lock: true })).toBe("lock");
    expect(repoMark({})).toBeUndefined();
  });
});

describe("skillFileProblemText", () => {
  it("说出是 SKILL.md 的哪一行、怎样改，不给原始报错", () => {
    expect(skillFileProblemText({ kind: SkillFileProblemKind.SkillFileUnquotedColon, line: 3 })).toBe(
      'SKILL.md 第 3 行：值里带「: 」，整个值要加上引号（如 description: "Use when: …"）',
    );
    expect(skillFileProblemText({ kind: SkillFileProblemKind.SkillFileNoDescription })).toBe("SKILL.md 的 frontmatter 里没有 description，或 description 不是文本");
    expect(skillFileProblemText({ kind: "later" as SkillFileProblemKind })).toBe("SKILL.md 写得不对");
  });

  it("SKILL.md 写错了的不能入库：整批入库的入口不算它", () => {
    expect(fetchable(unmanaged("ok", "claude-code"))).toBe(true);
    expect(fetchable({ ...unmanaged("broken", "claude-code"), invalid: { kind: SkillFileProblemKind.SkillFileInvalidYAML } })).toBe(false);
  });
});

describe("identicalState", () => {
  it("和库里一样、不是 app 装的那一份写明它已经在库里", () => {
    const u = { ...unmanaged("screenshot", "claude-code", 1), skill_id: 9, version_id: 3 };
    const state = identicalState({ id: 9, name: "screenshot", version_id: 3 } as Skill, u);
    expect(state.text).toBe("已在库里 · 这一份不是 app 装的");
    expect(state.stale).toBe(false);
  });
});

describe("extraCopy", () => {
  it("这里已经装着那个技能时，另一份一样的文件夹是多出来的一份，写明不用入库", () => {
    const copy = { ...unmanaged("pdf-copy", "claude-code", 1), skill_id: 9, version_id: 3 };
    const install = (agent: string, status = SkillScopeStatus.SkillScopeCurrent) => ({ skill_id: 9, scope: where(agent, 1), status }) as unknown as SkillScopeInspection;
    const installed = install("claude-code");
    expect(extraCopy(snapshot({ skillScopes: [] }), copy)).toBe(false);
    expect(extraCopy(snapshot({ skillScopes: [install("codex")] }), copy)).toBe(false);
    expect(extraCopy(snapshot({ skillScopes: [installed] }), copy)).toBe(true);
    expect(extraCopy(snapshot({ skillScopes: [installed] }), unmanaged("other", "claude-code", 1))).toBe(false);
    const skill = { id: 9, name: "pdf", version_id: 3 } as Skill;
    const state = identicalState(skill, copy, undefined, installed);
    expect(state.text).toBe("多一份一样的");
    expect(state.title).toContain("不用入库");
  });

  it("装着的那一处文件夹不在了时，不说多一份，只说这一份和库里的一样", () => {
    const copy = { ...unmanaged("my-pdf", "claude-code", 1), skill_id: 9, version_id: 3 };
    const gone = { skill_id: 9, scope: where("claude-code", 1), status: SkillScopeStatus.SkillScopeMissing } as unknown as SkillScopeInspection;
    const state = identicalState({ id: 9, name: "pdf", version_id: 3 } as Skill, copy, undefined, gone);
    expect(state.text).toBe("和库里的一样");
    expect(state.title).toContain("文件夹不在了");
  });
});

describe("identicalsOf", () => {
  it("共用一个目录的几个 Agent 那里是同一个文件夹，只算一处;同一处的几份各算一条", () => {
    const copy = (agent: string, folder: string, project_id?: number) => ({ ...unmanaged("pdf", agent, project_id), folder, skill_id: 9 });
    const s = snapshot({
      unmanaged: [
        copy("codex", "/web/.agents/skills/pdf", 1),
        copy("cursor", "/web/.agents/skills/pdf", 1),
        copy("codex", "/home/.codex/skills/pdf"),
        copy("codex", "/home/.codex/skills/pdf-copy"),
        unmanaged("other", "codex"),
      ],
    });
    expect(identicalsOf(s, 9).map((u) => u.folder)).toEqual(["/web/.agents/skills/pdf", "/home/.codex/skills/pdf", "/home/.codex/skills/pdf-copy"]);
  });
});

describe("identicalState 的悬停说明", () => {
  const skill = { id: 9, name: "screenshot", version_id: 3 } as Skill;
  const copy = { ...unmanaged("screenshot", "claude-code", 1), skill_id: 9, version_id: 3 };
  const lines = (u: UnmanagedSkill) => identicalState(skill, u, "/home").title.split("\n");

  it("Agent 目录之间的符号链接写出指向哪里", () => {
    expect(lines({ ...copy, link_target: "/home/.agents/skills/screenshot" }).filter((x) => x.startsWith("符号链接"))).toEqual(["符号链接，指向 /home/.agents/skills/screenshot"]);
  });

  it("指向 Agent 目录以外的只写一遍", () => {
    const outside = lines({ ...copy, link_outside: true, link_target: "/home/code/drafts/screenshot" });
    expect(outside.filter((x) => x.startsWith("符号链接"))).toEqual(["符号链接，指向 /home/code/drafts/screenshot：不在任何 Agent 的技能目录里，默认不入库"]);
  });
});

describe("handOverNote", () => {
  it("写出文件夹在哪个目录：一个 Agent 一个层级读几个目录时看得出是哪一个", () => {
    const skill = { ...unmanaged("foo", "gemini-cli"), folder: "/home/.gemini/skills/foo", directory: "/home/.gemini/skills" } as UnmanagedSkill;
    expect(handOverNote(snapshot({}), skill, "Gemini CLI · 全局")).toBe("入库就是把它交给 app 管。Gemini CLI · 全局 的文件夹 ~/.gemini/skills/foo 原样不动，Agent 用到的内容不变。");
  });
});
