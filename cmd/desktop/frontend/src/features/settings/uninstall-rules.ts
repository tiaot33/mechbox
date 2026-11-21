// 卸载 Mechbox(uninstall-dialog.tsx)里的判断：确认前说清有几个已安装的技能文件夹、在哪里，按选的处理方式会移走几个。
// 不碰界面、不调技能库，单元测试见 uninstall-rules.test.ts。
import { UninstallFolders } from "@bindings/cmd/desktop/models";
import { SkillScopeStatus, type ProjectID, type SkillScopeInspection } from "@bindings/internal/library";
import { folderPresent } from "@/lib/model";
import { isWSLPath } from "@/lib/paths";

// uninstallFolders 是现查出的各处安装(places)按文件夹合起来之后:present 是磁盘上在的文件夹，offMachine 是项目不在这台机器上的文件夹数，
// modified 是在的文件夹里被改过的;inGlobal / inProjects 是在全局、在项目里的文件夹数，projects 是涉及的项目。
// moving 是按 folders(保留、留下被改过的、全部移走)会移到废纸篓的文件夹数，movingInDistro 是其中在 WSL 发行版里的。
export function uninstallFolders(places: SkillScopeInspection[], folders: string) {
  const byFolder = new Map<string, SkillScopeInspection>();
  for (const p of places) if (!byFolder.has(p.folder)) byFolder.set(p.folder, p);
  const unique = [...byFolder.values()];
  const present = unique.filter(folderPresent);
  const modified = present.filter((p) => p.status === SkillScopeStatus.SkillScopeModified);
  const inGlobal = present.filter((p) => !p.scope.project_id).length;
  const moved = (p: SkillScopeInspection) =>
    folders === UninstallFolders.FoldersTrash || (folders === UninstallFolders.FoldersKeepModified && p.status !== SkillScopeStatus.SkillScopeModified);
  return {
    present,
    offMachine: unique.filter((p) => p.status === SkillScopeStatus.SkillScopeRootMissing).length,
    modified,
    inGlobal,
    inProjects: present.length - inGlobal,
    projects: [...new Set(present.flatMap((p): ProjectID[] => (p.scope.project_id ? [p.scope.project_id] : [])))],
    moving: present.filter(moved).length,
    movingInDistro: present.filter((p) => isWSLPath(p.folder) && moved(p)).length,
  };
}
