// 更新计划里的风险(新增或改动的脚本、新增的可执行文件、技能放宽了对 Agent 声明的权限)在界面上的说法。
// 「会自动调用它了」只是提示，不算风险：不进更新页那一行的风险摘要，更新计划里也和风险分开写。
import { UpdateRiskKind, type UpdateRisk } from "@bindings/internal/library";
import { hookText, toolText } from "@/lib/agent-settings";
import { messages } from "@/lib/i18n";

// isRiskNotice 是提示级的一条(原来关掉了自动调用，这次 Agent 会自动调用它了)。
export const isRiskNotice = (r: UpdateRisk) => r.kind === UpdateRiskKind.RiskAutoInvocationEnabled;

const settingKinds: string[] = [UpdateRiskKind.RiskAllowedToolsAdded, UpdateRiskKind.RiskHooksChanged, UpdateRiskKind.RiskToolsAdded];

// riskLabel 是一条风险的说法;agent 是技能对 Agent 声明的设置那几种说的那个 Agent 的名字。
export function riskLabel(r: UpdateRisk, agent = ""): string {
  const t = messages().update;
  const known: Record<string, string> = {
    [UpdateRiskKind.RiskExecutableAdded]: t.riskExecutableAdded,
    [UpdateRiskKind.RiskScriptAdded]: t.riskScriptAdded,
    [UpdateRiskKind.RiskScriptChanged]: t.riskScriptChanged,
    [UpdateRiskKind.RiskAllowedToolsAdded]: t.riskAllowedTools(agent),
    [UpdateRiskKind.RiskHooksChanged]: t.riskHooks(agent),
    [UpdateRiskKind.RiskToolsAdded]: t.riskTools(agent),
    [UpdateRiskKind.RiskAutoInvocationEnabled]: t.riskAutoInvocation(agent),
  };
  return known[r.kind] ?? t.riskOther;
}

// riskItems 是技能对 Agent 声明的设置里新增或改动的那几项，写法和技能详情里一样;脚本与可执行文件为空。
export function riskItems(r: UpdateRisk): string[] {
  return [...(r.items ?? []), ...(r.hooks ?? []).map(hookText), ...(r.tools ?? []).map(toolText)];
}

// fileRiskBadge 是差异文件列表里一个文件的标签：脚本与可执行文件照写;技能对 Agent 声明的设置合成一句「放宽了 Agent 权限」,
// 悬停逐条列出。只有提示级的一条时不用注意的颜色。
export function fileRiskBadge(risks: UpdateRisk[], agent: (r: UpdateRisk) => string): { label: string; title: string; notice: boolean } {
  const t = messages().update;
  const files = risks.filter((r) => !settingKinds.includes(r.kind) && !isRiskNotice(r)).map((r) => riskLabel(r));
  const settings = risks.some((r) => settingKinds.includes(r.kind));
  const labels = [...new Set([...files, ...(settings ? [t.riskLooser] : [])])];
  const notice = !labels.length;
  return {
    label: notice ? riskLabel(risks[0], agent(risks[0])) : labels.join(" · "),
    title: risks.map((r) => [riskLabel(r, agent(r)), ...riskItems(r)].join("\n  ")).join("\n"),
    notice,
  };
}

// riskSummary 把几处风险合成一句，如"新增 1 个脚本 · 改动 2 个脚本";没有风险(或只有提示)时为空。
export function riskSummary(risks: UpdateRisk[]): string {
  const count = (kind: UpdateRiskKind) => risks.filter((r) => r.kind === kind).length;
  return messages().update.riskSummary(
    count(UpdateRiskKind.RiskExecutableAdded),
    count(UpdateRiskKind.RiskScriptAdded),
    count(UpdateRiskKind.RiskScriptChanged),
    risks.filter((r) => settingKinds.includes(r.kind)).length,
  );
}
