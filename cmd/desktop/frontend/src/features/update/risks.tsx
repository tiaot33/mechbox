// 更新计划里的风险(新增或改动的脚本、新增的可执行文件、技能放宽了对 Agent 声明的权限)在界面上的说法。
// 「会自动调用它了」只是提示，不算风险：不进更新页那一行的风险摘要，更新计划里也和风险分开写。
import { AgentCode, UpdateRiskKind, type UpdateRisk } from "@bindings/internal/library";
import { messages } from "@/lib/i18n";

const agentNames: Record<string, string> = { [AgentCode.AgentClaudeCode]: "Claude Code", [AgentCode.AgentCodex]: "Codex" };

// isRiskNotice 是提示级的一条(原来只能手动调用，这次 Agent 会自动调用它了)。
export const isRiskNotice = (r: UpdateRisk) => r.kind === UpdateRiskKind.RiskAutoInvocationEnabled;

const settingKinds: string[] = [UpdateRiskKind.RiskAllowedToolsAdded, UpdateRiskKind.RiskHooksChanged, UpdateRiskKind.RiskToolsAdded];

export function riskLabel(r: UpdateRisk): string {
  const t = messages().update;
  const agent = agentNames[r.agent ?? ""] ?? r.agent ?? "";
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
