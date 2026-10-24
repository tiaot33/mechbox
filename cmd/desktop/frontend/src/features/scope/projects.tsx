// 已登记项目：登记、改位置、注销(注销时可选一并卸载装在项目里的技能)。
import { LibraryService } from "@bindings/cmd/desktop";
import { FolderPolicy, type RegisteredProject } from "@bindings/internal/library";
import { detachedBy } from "@/lib/consent";
import { messages } from "@/lib/i18n";
import { currentSnapshot } from "@/lib/library";
import { projectName, shortPath } from "@/lib/model";
import { ui } from "@/lib/ui";
import { Hint } from "@/components/ui/field";
import { DetachNotice } from "@/components/app/link-notes";
import { offerProject } from "@/features/fetch/fetch-unmanaged-all";
import { lostEdits } from "@/features/skill/remove";

// registerProject 登记项目;登记之后把用户带到这个项目里不是库装的技能，一次确认全部入库。
export async function registerProject(path: string): Promise<boolean> {
  const project = await ui.run(() => LibraryService.RegisterProject(path), {
    success: (p) => messages().actions.projectRegistered(p.path),
  });
  if (project) void offerProject(project);
  return !!project;
}

export async function relocateProject(project: RegisteredProject, path: string): Promise<boolean> {
  const moved = await ui.run(() => LibraryService.RelocateProject(project.id, path), {
    success: (p) => messages().actions.projectRelocated(p.name, p.path),
  });
  return !!moved;
}

export async function unregisterProject(project: RegisteredProject) {
  const s = currentSnapshot();
  const places = s.skillScopes.filter((p) => p.scope.project_id === project.id);
  const installed = places.length;
  const t = messages().actions;
  const choice = await ui.confirm({
    title: t.unregisterTitle(projectName(s, project.id)),
    body: (
      <>
        <Hint>{t.unregisterHint(shortPath(s, project.path))}</Hint>
        <DetachNotice items={detachedBy(places)} when="delete" prefix={t.ifRemoveToo} />
      </>
    ),
    choices: installed
      ? [
          {
            value: FolderPolicy.Remove,
            label: t.removeProjectFolders,
            desc: t.removeFolders(new Set(places.map((p) => p.folder)).size, lostEdits(places)),
          },
          { value: FolderPolicy.Keep, label: t.keepProjectFolders, desc: t.keepProjectFoldersDesc },
        ]
      : undefined,
    confirm: t.unregister,
    danger: true,
  });
  if (!choice) return;
  const policy = choice === FolderPolicy.Keep ? FolderPolicy.Keep : FolderPolicy.Remove;
  await ui.run(() => LibraryService.UnregisterProject(project.id, { folders: policy }), {
    success: t.unregistered,
  });
}

// 只有自定义 Agent 能改、能删。它离开或删除时，旧目录里还有别的 Agent 在读的文件夹不动，确认框里逐个列出。
