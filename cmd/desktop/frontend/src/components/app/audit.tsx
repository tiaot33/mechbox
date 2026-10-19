// skills.sh 的审计：入库预览里每个候选的标记，与技能详情里的审计卡片。只供参考，不影响入库、更新。
import { useEffect, useState } from "react";
import { Browser } from "@wailsio/runtime";
import { LibraryService } from "@bindings/cmd/desktop";
import { AuditAvailability, AuditStatus, type AuditEntry, type SkillAudit, type SkillID } from "@bindings/internal/library";
import { formatDate } from "@/lib/content";
import { messages, useT } from "@/lib/i18n";
import { Badge, type Tone } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { Hint } from "@/components/ui/field";
import { Spinner } from "@/components/ui/spinner";

// verdict 把各合作方的结论概括成一句：有未通过先说未通过，其次警告，否则通过。
function verdict(entries: AuditEntry[]): { tone: Tone; text: string } {
  const fail = entries.filter((e) => e.status === AuditStatus.AuditFail).length;
  const warn = entries.filter((e) => e.status === AuditStatus.AuditWarn).length;
  const t = messages().audit;
  if (fail) return { tone: "bad", text: t.failed(fail) };
  if (warn) return { tone: "warn", text: t.warned(warn) };
  return { tone: "ok", text: t.passed(entries.length) };
}

// statusBadge 是一个合作方结论的标签;skills.sh 以后加的新结论原样显示。
function statusBadge(status: string): { label: string; tone: Tone } | undefined {
  const t = messages().audit;
  const known: Record<string, { label: string; tone: Tone }> = {
    [AuditStatus.AuditPass]: { label: t.pass, tone: "ok" },
    [AuditStatus.AuditWarn]: { label: t.warn, tone: "warn" },
    [AuditStatus.AuditFail]: { label: t.fail, tone: "bad" },
  };
  return known[status];
}

// AuditBadge 把查到的审计概括成一个标记;不可用时点击重试，有结论时点击打开 skills.sh 的详情。
export function AuditBadge({ audit, onRetry }: { audit: SkillAudit | null; onRetry: (e: React.MouseEvent) => void }) {
  const t = useT().audit;
  if (!audit || audit.availability === AuditAvailability.AuditNone) return <Badge>{t.none}</Badge>;
  if (audit.availability === AuditAvailability.AuditUnavailable) {
    return (
      <Badge asChild>
        <button type="button" title={t.retryTitle} onClick={onRetry}>
          {t.unavailable}
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
  const t = useT().audit;
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
      <CardHeader title={t.title} note={t.note} />
      {audit === "loading" ? (
        <p className="flex items-center gap-1.5 text-muted-foreground">
          <Spinner />
          {t.loading}
        </p>
      ) : audit.availability === AuditAvailability.AuditUnavailable ? (
        <p className="text-muted-foreground">
          {t.unavailableLong}{" "}
          <Button variant="link" onClick={() => setAttempt((n) => n + 1)}>
            {t.retry}
          </Button>
        </p>
      ) : !available ? (
        <p className="text-muted-foreground">{t.noAudit}</p>
      ) : (
        <>
          <Badge tone={summary!.tone} className="mb-2">
            {summary!.text}
          </Badge>
          <table className="w-full border-collapse text-xs [&_td]:border-b [&_td]:border-border-subtle [&_td]:px-1.5 [&_td]:py-[7px] [&_td]:align-top">
            <tbody>
              {entries.map((e) => {
                const st = statusBadge(e.status) ?? { label: e.status, tone: "neutral" as Tone };
                return (
                  <tr key={e.slug || e.provider}>
                    <td className="font-medium whitespace-nowrap">{e.provider}</td>
                    <td>
                      <Badge tone={st.tone}>{st.label}</Badge>
                    </td>
                    <td className="w-full">
                      <div>{e.summary}</div>
                      <div className="text-[11px] text-muted-foreground">
                        {[e.risk_level && t.risk(e.risk_level), t.auditedAt(formatDate(e.audited_at))].filter(Boolean).join(" · ")}
                      </div>
                    </td>
                    <td className="whitespace-nowrap">
                      {e.detail_url && (
                        <Button variant="link" onClick={() => void Browser.OpenURL(e.detail_url!)}>
                          {t.details}
                        </Button>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
          <Hint className="mt-2">{t.scope}</Hint>
        </>
      )}
    </Card>
  );
}
