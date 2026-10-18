// skills.sh 的审计：入库预览里每个候选的标记，与技能详情里的审计卡片。只供参考，不影响入库、更新。
import { useEffect, useState } from "react";
import { Browser } from "@wailsio/runtime";
import { LibraryService } from "@bindings/cmd/desktop";
import { AuditAvailability, AuditStatus, type AuditEntry, type SkillAudit, type SkillID } from "@bindings/internal/library";
import { formatDate } from "@/lib/content";
import { Badge, type Tone } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { Hint } from "@/components/ui/field";
import { Spinner } from "@/components/ui/spinner";

// verdict 把各合作方的结论概括成一句：有未通过先说未通过，其次警告，否则通过。
function verdict(entries: AuditEntry[]): { tone: Tone; text: string } {
  const fail = entries.filter((e) => e.status === AuditStatus.AuditFail).length;
  const warn = entries.filter((e) => e.status === AuditStatus.AuditWarn).length;
  if (fail) return { tone: "bad", text: `审计 ${fail} 项未通过` };
  if (warn) return { tone: "warn", text: `审计 ${warn} 项警告` };
  return { tone: "ok", text: `审计通过 ${entries.length}` };
}

// statusBadge 是一个合作方结论的标签;skills.sh 以后加的新结论原样显示。
const statusBadge: Record<string, { label: string; tone: Tone }> = {
  [AuditStatus.AuditPass]: { label: "通过", tone: "ok" },
  [AuditStatus.AuditWarn]: { label: "警告", tone: "warn" },
  [AuditStatus.AuditFail]: { label: "未通过", tone: "bad" },
};

// AuditBadge 把查到的审计概括成一个标记;不可用时点击重试，有结论时点击打开 skills.sh 的详情。
export function AuditBadge({ audit, onRetry }: { audit: SkillAudit | null; onRetry: (e: React.MouseEvent) => void }) {
  if (!audit || audit.availability === AuditAvailability.AuditNone) return <Badge>无审计</Badge>;
  if (audit.availability === AuditAvailability.AuditUnavailable) {
    return (
      <Badge asChild>
        <button type="button" title="点击重试" onClick={onRetry}>
          审计不可用
        </button>
      </Badge>
    );
  }
  const entries = audit.entries ?? [];
  const { tone, text } = verdict(entries);
  const title = entries.map((e) => `${e.provider}:${e.status} ${e.summary}`).join("\n");
  const url = entries[0]?.detail_url;
  return (
    <Badge tone={tone} asChild>
      <button
        type="button"
        title={title}
        onClick={(e) => {
          e.preventDefault();
          if (url) void Browser.OpenURL(url);
        }}
      >
        {text}
      </button>
    </Badge>
  );
}

// AuditCard 是技能详情里的审计：打开详情就按技能的来源与名字查 skills.sh,逐个列出合作方的结论。
// 来源不在 skills.sh 收录范围(技能库返回 nil)时整张卡片不显示。
export function AuditCard({ skillID }: { skillID: SkillID }) {
  const [audit, setAudit] = useState<SkillAudit | null | "loading">("loading");
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    setAudit("loading");
    const call = LibraryService.GetSkillAudit(skillID);
    call.then(
      (a) => setAudit(a),
      () => setAudit({ availability: AuditAvailability.AuditUnavailable }),
    );
    return () => void call.cancel();
  }, [skillID, attempt]);
  if (audit === null) return null;
  const entries = audit === "loading" ? [] : (audit.entries ?? []);
  const available = audit !== "loading" && audit.availability === AuditAvailability.AuditAvailable;
  const summary = available ? verdict(entries) : null;
  return (
    <Card className="skill-audit">
      <CardHeader title="审计" note="来自 skills.sh · 只供参考" />
      {audit === "loading" ? (
        <p className="flex items-center gap-1.5 text-muted-foreground">
          <Spinner />
          正在查询 skills.sh…
        </p>
      ) : audit.availability === AuditAvailability.AuditUnavailable ? (
        <p className="text-muted-foreground">
          暂时查不到 skills.sh 的审计。{" "}
          <Button variant="link" onClick={() => setAttempt((n) => n + 1)}>
            重试
          </Button>
        </p>
      ) : !available ? (
        <p className="text-muted-foreground">skills.sh 上还没有这个技能的审计。</p>
      ) : (
        <>
          <Badge tone={summary!.tone} className="mb-2">
            {summary!.text}
          </Badge>
          <table className="w-full border-collapse text-xs [&_td]:border-b [&_td]:border-border-subtle [&_td]:px-1.5 [&_td]:py-[7px] [&_td]:align-top">
            <tbody>
              {entries.map((e) => {
                const st = statusBadge[e.status] ?? { label: e.status, tone: "neutral" as Tone };
                return (
                  <tr key={e.slug || e.provider}>
                    <td className="font-medium whitespace-nowrap">{e.provider}</td>
                    <td>
                      <Badge tone={st.tone}>{st.label}</Badge>
                    </td>
                    <td className="w-full">
                      <div>{e.summary}</div>
                      <div className="text-[11px] text-muted-foreground">
                        {[e.risk_level && `风险 ${e.risk_level}`, `审计于 ${formatDate(e.audited_at)}`].filter(Boolean).join(" · ")}
                      </div>
                    </td>
                    <td className="whitespace-nowrap">
                      {e.detail_url && (
                        <Button variant="link" onClick={() => void Browser.OpenURL(e.detail_url!)}>
                          详情
                        </Button>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
          <Hint className="mt-2">审计针对 skills.sh 收录的内容，不一定是库里的这一版。</Hint>
        </>
      )}
    </Card>
  );
}
