// 已登记项目：登记、改位置、注销(注销时可选一并卸载装在项目里的技能)。
import { LibraryService } from "@bindings/cmd/desktop";
import { FolderPolicy, type RegisteredProject } from "@bindings/internal/library";
import { detachedBy, discardedEdits, lostEdits } from "@/lib/consent";
import { messages } from "@/lib/i18n";
import { currentSnapshot } from "@/lib/library";
import { projectName } from "@/lib/scope";
import { wslLocation, shortPath } from "@/lib/paths";
import { ui } from "@/lib/ui";
import { Hint } from "@/components/ui/field";
import { DetachNotice, StaleNotes } from "@/components/app/link-notes";
import { unregisterDeletes } from "./projects-rules";
import { runDiscardingEdits } from "@/components/app/discard-edits";
import { offerProject } from "@/features/fetch/fetch-unmanaged-all";

// registerProject 登记项目;登记之后把用户带到这个项目里不是库装的技能，一次确认全部入库。
export async function registerProject(path: string): Promise<boolean> {
  const project = await ui.run(() => LibraryService.RegisterProject(path), {
    success: (p) => {
      const at = wslLocation(p.path);
      return at ? messages().actions.projectRegisteredInWSL(at.distro, at.path) : messages().actions.projectRegistered(p.path);
    },
  });
  if (project) void offerProject(project);
  return !!project;
}

export async function relocateProject(project: RegisteredProject, path: string): Promise<boolean> {
  const moved = await ui.run(() => LibraryService.RelocateProject(project.id, path), {
    success: (p) => {
      const at = wslLocation(p.path);
      return at ? messages().actions.projectRelocatedInWSL(p.name, at.distro, at.path) : messages().actions.projectRelocated(p.name, p.path);
    },
  });
  return !!moved;
}

// unregisterProject 注销项目。删掉的文件夹里哪几处被手动改过，按界面上已经读到的状态说;
// 确认之后才被改过的，技能库删之前核对出来再问一次(runDiscardingEdits)。
export async function unregisterProject(project: RegisteredProject) {
  const s = currentSnapshot();
  const { places } = unregisterDeletes(s, project.id);
  const installed = places.length;
  const t = messages().actions;
  const choice = await ui.confirm({
    title: t.unregisterTitle(projectName(s, project.id)),
    body: (
      <>
        <Hint>{t.unregisterHint(shortPath(s, project.path))}</Hint>
        <DetachNotice items={detachedBy(places)} when="delete" prefix={t.ifRemoveToo} />
        {/* 选同时删除时，技能库连同项目里指向这些文件夹的旧链接一起删掉：逐个写出。 */}
        <StaleNotes deleted={places} prefix={t.ifRemoveToo} copies={false} />
      </>
    ),
    choices: installed
      ? [
          {
            value: FolderPolicy.Remove,
            label: t.removeProjectFolders,
            desc: t.removeFolders(new Set(places.map((p) => p.folder)).size, lostEdits(places).length),
          },
          { value: FolderPolicy.Keep, label: t.keepProjectFolders, desc: t.keepProjectFoldersDesc },
        ]
      : undefined,
    confirm: t.unregister,
    danger: true,
  });
  if (!choice) return;
  const policy = choice === FolderPolicy.Keep ? FolderPolicy.Keep : FolderPolicy.Remove;
  await runDiscardingEdits(discardedEdits(places), (discard) => LibraryService.UnregisterProject(project.id, { folders: policy, discard_edits: discard }), {
    success: t.unregistered,
  });
}

// 只有自定义 Agent 能改、能删。它离开或删除时，旧目录里还有别的 Agent 在读的文件夹不动，确认框里逐个列出。
