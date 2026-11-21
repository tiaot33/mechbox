// 回滚：确认页列哪些技能、哪些地方，回滚之后只提示一句还是列出没装回的。
import { describe, expect, it } from "vitest";
import {
  AgentCode,
  ReinstallOutcome,
  RollbackOutcome,
  ScopeKind,
  type RollbackResult,
  type UpdateRecord,
} from "@bindings/internal/library";
import { putBack, recordChanged, recordPlaced, rolledBackFully } from "./rollback-rules";

const scope = { kind: ScopeKind.ScopeGlobal, agent: AgentCode.AgentClaudeCode };

describe("回滚确认页列什么", () => {
  const record = {
    id: 1,
    skills: [
      { skill_id: 1, from_version_id: 10, to_version_id: 11 },
      { skill_id: 2, from_version_id: 20, to_version_id: 20 },
    ],
    installs: [
      { skill_id: 1, scope, outcome: ReinstallOutcome.Reinstalled },
      { skill_id: 1, scope, outcome: ReinstallOutcome.ReinstallCurrent },
      { skill_id: 1, scope, outcome: ReinstallOutcome.ReinstallFailed },
    ],
  } as unknown as UpdateRecord;

  it("只列那次换了版本的技能和换上了的地方", () => {
    expect(recordChanged(record).map((x) => x.skill_id)).toEqual([1]);
    expect(recordPlaced(record).map((x) => x.outcome)).toEqual([ReinstallOutcome.Reinstalled, ReinstallOutcome.ReinstallCurrent]);
  });
});

describe("回滚之后", () => {
  const result = (outcomes: RollbackOutcome[], removed = 0) =>
    ({
      skills: [],
      installs: outcomes.map((outcome) => ({ skill_id: 1, scope, outcome })),
      removed: Array.from({ length: removed }, (_, i) => ({ skill_id: 10 + i, name: `r${i}` })),
    }) as unknown as RollbackResult;

  it("装回了旧版、本来就是旧版的算装回", () => {
    expect(putBack({ outcome: RollbackOutcome.RollbackReinstalled })).toBe(true);
    expect(putBack({ outcome: RollbackOutcome.RollbackCurrent })).toBe(true);
    expect(putBack({ outcome: RollbackOutcome.RollbackModified })).toBe(false);
  });

  it("都装回了、也没有技能已从库里移除时只提示一句;否则列出原因", () => {
    expect(rolledBackFully(result([RollbackOutcome.RollbackReinstalled, RollbackOutcome.RollbackCurrent]))).toBe(true);
    expect(rolledBackFully(result([]))).toBe(true);
    expect(rolledBackFully(result([RollbackOutcome.RollbackReinstalled, RollbackOutcome.RollbackMissing]))).toBe(false);
    expect(rolledBackFully(result([RollbackOutcome.RollbackReinstalled], 1))).toBe(false);
  });
});
