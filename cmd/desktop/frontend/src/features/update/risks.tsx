// 更新计划里的风险(新增或改动的脚本、新增的可执行文件)在界面上的说法。
import { UpdateRiskKind, type UpdateRisk } from "@bindings/internal/library";
import { messages } from "@/lib/i18n";

export function riskLabel(r: UpdateRisk): string {
  const t = messages().update;
  const known: Record<string, string> = {
    [UpdateRiskKind.RiskExecutableAdded]: t.riskExecutableAdded,
    [UpdateRiskKind.RiskScriptAdded]: t.riskScriptAdded,
    [UpdateRiskKind.RiskScriptChanged]: t.riskScriptChanged,
  };
  return known[r.kind] ?? t.riskOther;
}

// riskSummary 把几处风险合成一句，如"新增 1 个脚本 · 改动 2 个脚本";没有风险时为空。
export function riskSummary(risks: UpdateRisk[]): string {
  const count = (kind: UpdateRiskKind) => risks.filter((r) => r.kind === kind).length;
  return messages().update.riskSummary(
    count(UpdateRiskKind.RiskExecutableAdded),
    count(UpdateRiskKind.RiskScriptAdded),
    count(UpdateRiskKind.RiskScriptChanged),
  );
}

