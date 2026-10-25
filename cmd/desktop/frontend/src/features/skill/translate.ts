// 翻译技能描述：技能详情里点「翻译」每次都重新翻译;自动翻译在主界面后台把还没有译文的描述一批批交给技能库。
// 译文由技能库存在数据库里，快照里的 Skill.description_translations 按界面语言取。
import { useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import { LibraryService } from "@bindings/cmd/desktop";
import { ErrorCode, type Skill } from "@bindings/internal/library";
import { aiError, describeError } from "@/lib/errors";
import { currentLanguage, messages, useLanguage } from "@/lib/i18n";
import { aiSettingsQuery, queryClient, reloadLibrary, snapshotQuery, toLibraryError, useLoadedSnapshot, type Snapshot } from "@/lib/library";
import { ui } from "@/lib/ui";

export async function translateDescription(skill: Skill): Promise<boolean> {
  const t = messages().drawer;
  const done = await ui.run(() => LibraryService.TranslateDescription(skill.id, currentLanguage()), {
    onError: (err) => {
      const text = aiError(err);
      if (!text) return false;
      ui.toast(t.translateFailed(text), "error");
      return true;
    },
  });
  return done !== undefined;
}

// 自动翻译每批交几个技能：翻完一批重读一次，列表上的描述一批批换成译文。
const batchSize = 8;
// asked 记下交过的(技能、语言、原文):技能库跳过已经是界面语言的描述，不存译文，记下来免得每次重读都再交一遍。
const asked = new Set<string>();
let running = false;
// stoppedAt 是自动翻译失败时的 AI 设置：设置改了才再试，没改就等下次打开 app。
let stoppedAt: string | undefined;

const askKey = (skill: Skill, language: string) => `${skill.id}\n${language}\n${skill.description}`;
const settingsKey = () => JSON.stringify(queryClient.getQueryData(aiSettingsQuery.queryKey) ?? null);

function waiting(s: Snapshot, language: string): Skill[] {
  return s.skills.filter((x) => x.description && !x.description_translations?.[language] && !asked.has(askKey(x, language)));
}

// useAutoTranslate 挂在主界面：开着自动翻译、填了 AI 模型时，快照或界面语言变了就看有没有要翻译的描述。
export function useAutoTranslate() {
  const s = useLoadedSnapshot();
  const { language } = useLanguage();
  const ai = useQuery(aiSettingsQuery).data;
  const ready = !!s?.autoTranslate && !!ai?.model && JSON.stringify(ai) !== stoppedAt;
  useEffect(() => {
    if (ready) void autoTranslate();
  }, [s, language, ready]);
}

async function autoTranslate() {
  if (running) return;
  running = true;
  try {
    for (;;) {
      const s = queryClient.getQueryData(snapshotQuery.queryKey);
      const ai = queryClient.getQueryData(aiSettingsQuery.queryKey);
      const language = currentLanguage();
      if (!s?.autoTranslate || !ai?.model || settingsKey() === stoppedAt) return;
      const batch = waiting(s, language).slice(0, batchSize);
      if (!batch.length) return;
      batch.forEach((x) => asked.add(askKey(x, language)));
      try {
        const translated = await LibraryService.TranslateDescriptions(language, batch.map((x) => x.id));
        if (translated) await reloadLibrary();
      } catch (e) {
        // 这一批里已翻译的都已存下;没翻译的等 AI 设置改了再交。
        batch.forEach((x) => asked.delete(askKey(x, language)));
        await reloadLibrary();
        const err = toLibraryError(e);
        if (err.code === ErrorCode.Canceled) return;
        stoppedAt = settingsKey();
        ui.toast(messages().app.autoTranslateFailed(aiError(err) ?? describeError(err)), "error");
        return;
      }
    }
  } finally {
    running = false;
  }
}
