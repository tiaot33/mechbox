// 执行更新(issue #131):按更新计划一次完成更新和重新安装(Update),以及把没换上的几处再换一次。
// 更新计划、用目录里的修改更新两个弹窗与不开弹窗的快捷更新(update.ts)都经 runUpdate 执行;汇报(report.tsx)和更新历史里的重试经 placeAgain。
// 默认勾哪些地方见 lib/consent.ts,执行结果怎么归并见 update-rules.ts。
import type { CancellablePromise } from "@wailsio/runtime";
import { LibraryService } from "@bindings/cmd/desktop";
import {
  ReinstallOutcome,
  type ReinstallRequest,
  type ReinstallResult,
  type SkillID,
  type SkillScopeInspection,
  type UpdatePlan,
  type UpdateRecordID,
} from "@bindings/internal/library";
import { updateInstalls } from "@/lib/consent";
import { messages } from "@/lib/i18n";
import { currentSnapshot } from "@/lib/library";
import { isLocal } from "@/lib/source";
import { ui } from "@/lib/ui";
import { confirmLinkWrites } from "@/components/app/link-notes";
import { failedOnly, stalePlan, updateOutcome, type UpdateOutcome } from "./update-rules";

// runUpdate 按更新计划执行一次更新：库先换(全成或全不成),勾选的地方再逐处换。
// 计划生成后来源或库变了时返回 "conflict",由调用方让用户重新看计划;其他失败已提示错误，返回 undefined。
export async function runUpdate(
  plan: UpdatePlan,
  label: string,
  checked: Set<SkillID>,
  installOn: (p: SkillScopeInspection) => boolean,
  from?: string,
  confirmLinks = false, // 不经更新计划弹窗的快捷更新：写到符号链接相关的地方前先确认
): Promise<UpdateOutcome | "conflict" | undefined> {
  const s = currentSnapshot();
  const picked = updateInstalls(plan, checked, installOn);
  const { places, written, requests } = picked;
  if (confirmLinks && !(await confirmLinkWrites(places, written, messages().update.confirmTitle(label), messages().update.update))) return undefined;
  let conflict = false;
  const result = await ui.run(() => LibraryService.Update({ plan: plan.id, skills: [...checked], installs: requests }), {
    label: from ? messages().update.fromScopeLabel(from, label) : isLocal(plan.source) ? messages().update.rereadLabel(label) : messages().update.updateLabel([label]),
    onError: (err) => (conflict = stalePlan(err)),
    failed: (r) => (r.installs ?? []).some((x) => x.outcome === ReinstallOutcome.ReinstallFailed),
  });
  if (conflict) return "conflict";
  if (!result) return undefined;
  return updateOutcome(s, plan, label, checked, picked, result, from);
}

// placeAgain 把这几处换成当前版本:record 不为 0 时结果算进那次更新(回滚时一起装回去),否则是普通的重新安装。
// 返回与 requests 一一对应的结果;调用失败(含取消)时返回 undefined。
export function placeAgain(record: UpdateRecordID, requests: ReinstallRequest[], track?: (call: CancellablePromise<ReinstallResult[] | null>) => void) {
  return ui.run(() => {
    const call = record ? LibraryService.RetryUpdateInstalls(record, requests) : LibraryService.Reinstall(requests);
    track?.(call);
    return call;
  }, { failed: (r) => failedOnly(r).length > 0, label: messages().update.retryLabel });
}
