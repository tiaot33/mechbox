// 判断关系图里单个词的技能名：打开某个集合或组的关系图时，把还没判断的几处交给技能库用 AI 判断(不在后台判断全库)。
// 结论由技能库按技能版本存进数据库，判断完重读关系图，补上判断为提到的线、重新排一次。
import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { LibraryService } from "@bindings/cmd/desktop";
import { ErrorCode, type JudgeMentionsResult, type MentionsFilter } from "@bindings/internal/library";
import { aiError, describeError } from "@/lib/errors";
import { aiSettingsQuery, queryClient, toLibraryError } from "@/lib/library";

// Judging 是这次打开关系图时判断的进度：
// judging 正在判断这么多处;done 判断完了，failed 是只因自己没判断成、跳过的技能;stopped 是整体失败停下了。
export type Judging =
  | { state: "idle" }
  | { state: "judging"; count: number }
  | { state: "done"; failed: JudgeMentionsResult["failed"]; reason?: string }
  | { state: "stopped"; reason: string };

// running 记下正在判断的集合或组：关系图关上又打开时接着等同一次判断，不重复发请求。
const running = new Map<string, Promise<Judging>>();

async function judge(filter: MentionsFilter): Promise<Judging> {
  try {
    const result = await LibraryService.JudgeMentions(filter);
    const first = result.failed?.[0]?.error;
    return { state: "done", failed: result.failed, reason: first && (aiError(first) ?? describeError(first)) };
  } catch (e) {
    const err = toLibraryError(e);
    if (err.code === ErrorCode.Canceled) return { state: "idle" };
    return { state: "stopped", reason: aiError(err) ?? describeError(err) };
  } finally {
    // 判断过的已存下(整体失败时也保留已判断的),所有关系图都重读一次。
    await queryClient.invalidateQueries({ queryKey: ["mentions"] });
  }
}

// useJudgeMentions 挂在关系图页上：填了 AI 模型、还有没判断的几处时判断一次;这次打开里不再重试，
// 只因自己没判断成的技能和整体失败都留到下次打开关系图再试。没填模型时不判断，界面提示去设置。
export function useJudgeMentions(filter: MentionsFilter, unjudged: number | undefined): Judging {
  const key = JSON.stringify(filter);
  const model = !!useQuery(aiSettingsQuery).data?.model;
  const [judging, setJudging] = useState<Judging>({ state: "idle" });
  const started = useRef<string | null>(null);
  useEffect(() => {
    if (!model || !unjudged || started.current === key) return;
    started.current = key;
    let live = true;
    setJudging({ state: "judging", count: unjudged });
    let run = running.get(key);
    if (!run) {
      run = judge(filter).finally(() => running.delete(key));
      running.set(key, run);
    }
    void run.then((outcome) => live && setJudging(outcome));
    // 判断完重读后没有要判断的了、或判断途中清掉了模型时，回到 idle 由界面按最新的 Unjudged 提示。
    return () => {
      live = false;
      started.current = null;
      setJudging({ state: "idle" });
    };
  }, [key, model, !!unjudged]);
  return judging;
}
