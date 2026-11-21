// 卸载 Mechbox:确认前说的文件夹数，按选的处理方式会移走几个。
import { describe, expect, it } from "vitest";
import { UninstallFolders } from "@bindings/cmd/desktop/models";
import { AgentCode, ScopeKind, SkillScopeStatus, type Scope, type SkillScopeInspection } from "@bindings/internal/library";
import { uninstallFolders } from "./uninstall-rules";

const claude = AgentCode.AgentClaudeCode;
const codex = AgentCode.AgentCodex;
const global = (agent: AgentCode): Scope => ({ kind: ScopeKind.ScopeGlobal, agent });
const project = (agent: AgentCode, project_id: number): Scope => ({ kind: ScopeKind.ScopeProject, agent, project_id });
const place = (folder: string, scope: Scope, status = SkillScopeStatus.SkillScopeCurrent): SkillScopeInspection => ({ skill_id: 1, scope, folder, direct: true, status });

const places = [
  place("/home/.claude/skills/a", global(claude)),
  // 全局共用的文件夹只算一个。
  place("/home/.claude/skills/a", global(codex)),
  place("/home/.claude/skills/b", global(claude), SkillScopeStatus.SkillScopeModified),
  place("/p7/.claude/skills/a", project(claude, 7)),
  place("\\\\wsl.localhost\\Ubuntu\\home\\u\\.claude\\skills\\c", global(claude)),
  place("/p8/.claude/skills/a", project(claude, 8), SkillScopeStatus.SkillScopeRootMissing),
  place("/p7/.claude/skills/gone", project(claude, 7), SkillScopeStatus.SkillScopeMissing),
];

describe("卸载 Mechbox 时的已安装文件夹", () => {
  it("按文件夹算：在磁盘上的分全局和项目，项目不在这台机器上的另说，文件夹不在了的不算", () => {
    const r = uninstallFolders(places, UninstallFolders.FoldersKeep);
    expect(r.present).toHaveLength(4);
    expect(r).toMatchObject({ offMachine: 1, inGlobal: 3, inProjects: 1, projects: [7] });
    expect(r.modified.map((p) => p.folder)).toEqual(["/home/.claude/skills/b"]);
  });

  it("保留时不移走;留下被改过的时移走其余的;全部移走时都移走，其中在 WSL 发行版里的另说", () => {
    expect(uninstallFolders(places, UninstallFolders.FoldersKeep)).toMatchObject({ moving: 0, movingInDistro: 0 });
    expect(uninstallFolders(places, UninstallFolders.FoldersKeepModified)).toMatchObject({ moving: 3, movingInDistro: 1 });
    expect(uninstallFolders(places, UninstallFolders.FoldersTrash)).toMatchObject({ moving: 4, movingInDistro: 1 });
  });
});
