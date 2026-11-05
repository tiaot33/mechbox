// 判断关系图里单个词的技能名：打开某个集合或组的关系图时，把还没判断的几处交给技能库用 AI 判断(不在后台判断全库)。
// 结论由技能库按技能版本存进数据库，判断完重读关系图，补上判断为提到的线、重新排一次。
import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { LibraryService } from "@bindings/cmd/desktop";
import { ErrorCode, type JudgeMentionsResult, type MentionsFilter } from "@bindings/internal/library";
import { aiError, describeError } from "@/lib/errors";
import { aiSettingsQuery, queryClient, toLibraryError } from "@/lib/library";

// Outcome 是一次判断的结果：done 判断完了，failed 是只因自己没判断成、跳过的技能;
// stopped 是整体失败停下了，fix 说怎样再判断：settings 要先改 AI 设置(Key 被拒、找不到模型等),retry 过一会儿重试就可能好(连不上、超时、限流等)。
type Outcome =
  | { state: "done"; failed: JudgeMentionsResult["failed"]; reason?: string }
  | { state: "stopped"; reason: string; fix: "settings" | "retry" };

// Judging 是关系图页顶部要显示的判断状态：none 没有要提示的;unconfigured 没配 AI 模型;judging 正在判断这么多处。
export type Judging = { state: "none" } | { state: "unconfigured"; count: number } | { state: "judging"; count: number } | Outcome;

// 要改 AI 设置才会好的错误;其余(连不上、超时、限流、数据库被拉回换掉等)过一会儿重试。
const needsSettings = new Set<string>([ErrorCode.Permission, ErrorCode.NotFound, ErrorCode.NotConfigured, ErrorCode.InvalidArgument]);

// running 记下正在判断的集合或组：关系图关上又打开时接着等同一次判断，不重复发请求。
const running = new Map<string, Promise<Outcome>>();

async function judge(filter: MentionsFilter): Promise<Outcome> {
  try {
    const result = await LibraryService.JudgeMentions(filter);
    const first = result.failed?.[0]?.error;
    return { state: "done", failed: result.failed, reason: first && (aiError(first) ?? describeError(first)) };
  } catch (e) {
    const err = toLibraryError(e);
    return { state: "stopped", reason: aiError(err) ?? describeError(err), fix: needsSettings.has(err.code) ? "settings" : "retry" };
  } finally {
    // 判断过的已存下(整体失败时也保留已判断的),所有关系图都重读一次。
    await queryClient.invalidateQueries({ queryKey: ["mentions"] });
  }
}

// useJudgeMentions 挂在关系图页上：填了 AI 模型、还有没判断的几处时判断一次;这次打开里不自动再试，
// 只因自己没判断成的技能留到下次打开关系图，整体失败时按 fix 去设置或调 retry 重试。没填模型时不判断，返回 unconfigured。
export function useJudgeMentions(filter: MentionsFilter, unjudged: number): { judging: Judging; retry: () => void } {
  const key = JSON.stringify(filter);
  const ai = useQuery(aiSettingsQuery);
  const model = !!ai.data?.model;
  // outcome 是这一轮判断的结果，开始一轮时清掉;attempt 每重试一次加 1。
  const [outcome, setOutcome] = useState<Outcome | null>(null);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!model || !unjudged) return;
    let live = true;
    setOutcome(null);
    let run = running.get(key);
    if (!run) {
      run = judge(filter).finally(() => running.delete(key));
      running.set(key, run);
    }
    void run.then((result) => live && setOutcome(result));
    return () => void (live = false);
  }, [key, model, !!unjudged, attempt]);

  let judging: Judging;
  if (!unjudged || !ai.isSuccess) judging = { state: "none" };
  else if (!model) judging = { state: "unconfigured", count: unjudged };
  else judging = outcome ?? { state: "judging", count: unjudged };
  return { judging, retry: () => setAttempt((n) => n + 1) };
}
