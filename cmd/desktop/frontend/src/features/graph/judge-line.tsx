// 单个词的技能名交给 AI 判断时的状态行：关系图顶部与技能详情的提到关系共用，文案由调用方给。
import type { Messages } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import { cn } from "@/lib/utils";
import { openSettings } from "@/lib/view";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { Tipped } from "@/components/app/parts";
import type { Judging } from "./judge";

export type JudgeTexts = Pick<
  Messages["graph"],
  | "judging"
  | "judgingShort"
  | "unjudged"
  | "unjudgedShort"
  | "setUpAI"
  | "retry"
  | "judgeStoppedShort"
  | "judgeStopped"
  | "judgeNeedsSettings"
  | "judgeSkippedShort"
  | "judgeSkipped"
>;

// JudgeLine 是单个词的技能名交给 AI 判断的状态：判断中写出还有几处;没配 AI 时提示去设置;
// 只因自己没判断成的技能写出原因;整体失败时写出原因，要改 AI 设置的给「去设置」，过一会儿可能就好的给「重试」。都没有时不占位置。
// t 是这一处的说法(关系图写「画线」,技能详情写「列出」)。
export function JudgeLine({ judging, unjudged, retry, t, className }: { judging: Judging; unjudged: number; retry: () => void; t: JudgeTexts; className?: string }) {
  const s = useSnapshot();
  let text: string;
  let reason: string | undefined;
  let action: { label: string; run: () => void; away?: boolean } | undefined;
  switch (judging.state) {
    case "judging":
      [text, reason] = [t.judgingShort(judging.count), t.judging(judging.count)];
      break;
    case "unconfigured":
      [text, reason, action] = [t.unjudgedShort(judging.count), t.unjudged(judging.count), { label: t.setUpAI, run: () => openSettings("ai"), away: true }];
      break;
    case "stopped":
      [text, reason] = [t.judgeStoppedShort(unjudged), [judging.fix === "settings" ? t.judgeNeedsSettings(unjudged) : t.judgeStopped(unjudged), judging.reason].filter(Boolean).join("\n")];
      action = judging.fix === "settings" ? { label: t.setUpAI, run: () => openSettings("ai"), away: true } : { label: t.retry, run: retry };
      break;
    case "done": {
      if (!judging.failed?.length) return null;
      const names = judging.failed.map((f) => s.skills.find((x) => x.id === f.skill_id)?.name ?? `#${f.skill_id}`);
      [text, reason] = [t.judgeSkippedShort(names.length), [t.judgeSkipped(names), judging.reason].filter(Boolean).join("\n")];
      break;
    }
    default:
      return null;
  }
  const warn = judging.state === "stopped" || judging.state === "done";
  // 一行写完：短句在前，原因和细节在悬停提示里。
  return (
    <div
      className={cn("graph-judge flex min-w-0 items-center gap-2 text-caption", warn ? "text-foreground" : "text-muted-foreground", className)}
      data-state={judging.state}
    >
      {judging.state === "judging" && <Spinner />}
      <Tipped tip={reason && <span className="whitespace-pre-line select-text">{reason}</span>} className="min-w-0">
        <span className="min-w-0 truncate">{text}</span>
      </Tipped>
      {action && (
        <Button variant={action.away ? "link" : "ghost"} size="inline" className="graph-judge-action shrink-0 text-caption" onClick={action.run}>
          {action.label}
        </Button>
      )}
    </div>
  );
}

