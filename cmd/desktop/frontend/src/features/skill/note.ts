// 技能的备注：只存在数据库里，技能详情里写和看，列表里鼠标停在技能名字上看。
import { LibraryService } from "@bindings/cmd/desktop";
import type { SkillID } from "@bindings/internal/library";
import { messages } from "@/lib/i18n";
import { ui } from "@/lib/ui";

export async function saveNote(id: SkillID, note: string): Promise<boolean> {
  const saved = await ui.run(() => LibraryService.SetSkillNote(id, note).then(() => true), { success: messages().drawer.noteSaved });
  return !!saved;
}
