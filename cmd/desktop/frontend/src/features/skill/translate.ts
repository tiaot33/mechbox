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
// 连不上、超时、限流时过多久再试。
const retryDelay = 60_000;
// asked 记下这次运行里交过的(技能、语言、原文):技能库跳过已经是界面语言的描述、只因这一条没翻译成的描述，都不存译文，
// 记下来免得每次重读都再交一遍。
const asked = new Set<string>();
let running = false;
// stoppedAt 是整体性失败(Key 被拒、模型不存在等)时的 AI 设置：设置改了、开关重新打开或重开 app 才再试。
let stoppedAt: string | undefined;
// pausedUntil 之前不交：连不上、超时、限流之后过一会儿自己再试，只提示第一次。
let pausedUntil = 0;
let retryTimer: number | undefined;
let warnedUnreachable = false;

const askKey = (skill: Skill, language: string) => `${skill.id}\n${language}\n${skill.description}`;
const settingsKey = () => JSON.stringify(queryClient.getQueryData(aiSettingsQuery.queryKey) ?? null);

function waiting(s: Snapshot, language: string): Skill[] {
  return s.skills.filter((x) => x.description && !x.description_translations?.[language] && !asked.has(askKey(x, language)));
}

// resumeAutoTranslate 在设置里重新打开自动翻译时调用：之前因失败停下的，回到主界面后再试。
export function resumeAutoTranslate() {
  stoppedAt = undefined;
  pausedUntil = 0;
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
  if (running || Date.now() < pausedUntil) return;
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
        const result = await LibraryService.TranslateDescriptions(language, batch.map((x) => x.id));
        // 只因这一条没翻译成的留在 asked 里，这次不再交，其余照常翻译。
        if (result.failed?.length) {
          const names = result.failed.map((f) => s.skills.find((x) => x.id === f.skill_id)?.name ?? String(f.skill_id));
          const reason = aiError(result.failed[0].error) ?? describeError(result.failed[0].error);
          ui.toast(messages().app.autoTranslateSkipped(names, reason), "error");
        }
        if (result.translated) await reloadLibrary();
      } catch (e) {
        // 这一批里已翻译的都已存下;没翻译的之后再交。
        batch.forEach((x) => asked.delete(askKey(x, language)));
        await reloadLibrary();
        const err = toLibraryError(e);
        if (err.code === ErrorCode.Canceled) return;
        const reason = aiError(err) ?? describeError(err);
        if (err.code === ErrorCode.SourceUnreachable) {
          pausedUntil = Date.now() + retryDelay;
          window.clearTimeout(retryTimer);
          retryTimer = window.setTimeout(() => void autoTranslate(), retryDelay);
          if (!warnedUnreachable) ui.toast(messages().app.autoTranslatePaused(reason), "error");
          warnedUnreachable = true;
          return;
        }
        stoppedAt = settingsKey();
        ui.toast(messages().app.autoTranslateFailed(reason), "error");
        return;
      }
    }
  } finally {
    running = false;
  }
}
