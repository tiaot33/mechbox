// 给没有来源的技能接上来源记录里记着的来源：确认后接上，之后它跟着这个来源检查更新、更新。
import { LibraryService } from "@bindings/cmd/desktop";
import { ErrorCode, type Skill } from "@bindings/internal/library";
import { attachSourceError } from "@/lib/errors";
import { messages } from "@/lib/i18n";
import type { RecordedSource } from "@/lib/source";
import { ui } from "@/lib/ui";
import { openDrawer } from "@/lib/view";

export async function attachSource(skill: Skill, recorded: RecordedSource): Promise<void> {
  const t = messages().drawer;
  const source = recorded.ref ? `${recorded.input} · ${recorded.ref}` : recorded.input;
  const ok = await ui.confirm({ title: t.attachSourceTitle(skill.name, source), body: t.attachSourceBody, confirm: t.attachSource });
  if (!ok) return;
  await ui.run(() => LibraryService.AttachSource({ skill_id: skill.id, source_input: recorded.input, source_ref: recorded.ref }), {
    label: t.attachingSource(skill.name),
    success: t.attachedSource(skill.name, source),
    onError: (err) => {
      // 集合里已有同名的那一个时，toast 上能点开它。
      const other = err.code === ErrorCode.AlreadyExists ? err.details?.skill_id : undefined;
      ui.toast(attachSourceError(err, skill.name), "error", other ? { label: t.openTwin(skill.name), onClick: () => openDrawer(other) } : undefined);
      return true;
    },
  });
}
