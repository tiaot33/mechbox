// 技能详情里的「在 Agent 里怎样用」:技能对 Claude Code、Codex 声明的设置，每个 Agent 一节，只列和默认不同的。
// 从作用域页打开时，读那个作用域的 Agent 的一节排在前面。全是默认时不显示这张卡片。
import { AgentCode, type Skill } from "@bindings/internal/library";
import { settingSections } from "@/lib/agent-settings";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import { agentName, parseScope, scopeReaders } from "@/lib/model";
import { useView } from "@/lib/view";
import { cn } from "@/lib/utils";
import { Card, CardHeader } from "@/components/ui/card";
import { AgentMark } from "@/components/app/parts";

const builtIn: AgentCode[] = [AgentCode.AgentClaudeCode, AgentCode.AgentCodex];

export function AgentSettingsCard({ skill }: { skill: Skill }) {
  const t = useT().agentSettings;
  const s = useSnapshot();
  const page = useView((v) => v.page);
  const first = page.kind === "scope" ? scopeReaders(s, parseScope(page.scope)).find((code) => builtIn.includes(code)) : undefined;
  const sections = settingSections(skill.agent_settings, skill.name, first);
  if (!sections.length) return null;
  const name = (code: AgentCode) => agentName(s, code);
  return (
    <Card className="agent-settings-card">
      <CardHeader title={t.title} note={<span title={t.noteTitle}>{t.note}</span>} />
      <div className="flex flex-col gap-3">
        {sections.map((section) => (
          <div key={section.agent} className="agent-settings-section" data-agent={section.agent}>
            <div className="mb-1.5 flex items-center gap-1.5 font-semibold">
              <AgentMark name={name(section.agent)} />
              {name(section.agent)}
            </div>
            <dl className="grid grid-cols-[104px_minmax(0,1fr)] [&:lang(en)]:grid-cols-[136px_minmax(0,1fr)] gap-x-3.5 gap-y-1.5 [&_dt]:text-muted-foreground">
              {section.rows.map((row) => (
                <div key={row.key} className="contents" data-setting={row.key}>
                  <dt>{row.label}</dt>
                  <dd className="flex min-w-0 flex-col gap-0.5">
                    {row.values.map((value, i) => (
                      <span key={i} className={cn("break-words", row.mono && "font-mono text-caption")}>
                        {value}
                      </span>
                    ))}
                  </dd>
                </div>
              ))}
            </dl>
          </div>
        ))}
      </div>
    </Card>
  );
}
