// 回滚(rollback.tsx)里的判断：确认页列哪些技能、哪些地方，回滚之后是一句提示就关上，还是换成结果页列出没装回的。
// 不碰界面、不调技能库，单元测试见 rollback-rules.test.ts。
import { ReinstallOutcome, RollbackOutcome, type RollbackResult, type UpdateRecord } from "@bindings/internal/library";

// putBack 是回滚时这一处装回了旧版(或本来就是旧版)。
export const putBack = (r: { outcome: string }) => r.outcome === RollbackOutcome.RollbackReinstalled || r.outcome === RollbackOutcome.RollbackCurrent;

// recordPlaced 是那次更新换上了的地方：回滚时要装回旧版的就是这些(之后又重新安装过、被改过的由回滚核对后不动)。
export const recordPlaced = (record: UpdateRecord) =>
  (record.installs ?? []).filter((i) => i.outcome === ReinstallOutcome.Reinstalled || i.outcome === ReinstallOutcome.ReinstallCurrent);

// recordChanged 是那次更新换了版本的技能：回滚会把它们换回去;版本没变的不列。
export const recordChanged = (record: UpdateRecord) => (record.skills ?? []).filter((x) => x.from_version_id !== x.to_version_id);

// rolledBackFully 是回滚之后都装回了、也没有技能已从库里移除：只提示一句就关上，否则换成结果页列出原因。
export const rolledBackFully = (result: RollbackResult) => (result.installs ?? []).every(putBack) && !(result.removed ?? []).length;
