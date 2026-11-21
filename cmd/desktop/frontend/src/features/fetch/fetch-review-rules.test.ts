// 入库预览：分组标题上的勾选，勾上的候选提到的没勾的候选。
import { describe, expect, it } from "vitest";
import type { Candidate, Skill } from "@bindings/internal/library";
import type { Snapshot } from "@/lib/library";
import { libraryTwins, toggleCandidates, unpickedMentions } from "./fetch-review-rules";

const candidate = (name: string, extra: Partial<Candidate> = {}) => ({ name, path: name, ...extra }) as Candidate;

describe("分组标题上的勾选", () => {
  const members = [candidate("a"), candidate("b"), candidate("old", { fetched: 3 })];

  it("组里没入库的都勾上时取消全组，否则全组勾上;已入库的不动", () => {
    expect([...toggleCandidates(new Set(["a"]), members)].sort()).toEqual(["a", "b"]);
    expect([...toggleCandidates(new Set(["a", "b", "x"]), members)]).toEqual(["x"]);
  });
});

describe("勾上的候选提到的没勾的候选", () => {
  const candidates = [
    candidate("a", { mentions: ["b", "c", "d", "lib", "nope"] }),
    candidate("b"),
    candidate("c"),
    candidate("d", { fetched: 3 }),
    candidate("lib"),
  ];
  const s = { skills: [{ id: 9, name: "lib" } as Skill] } as unknown as Snapshot;

  it("只说同一份预览里没勾、还没入库、库里也没有同名的", () => {
    expect(unpickedMentions(s, candidates, new Set(["a", "c"]), candidates[0])).toEqual(["b"]);
  });

  it("库里的同名技能：还没入库的候选才算，库还没读到时没有", () => {
    expect(libraryTwins(s, candidates[4]).map((x) => x.id)).toEqual([9]);
    expect(libraryTwins(s, candidate("lib", { fetched: 9 }))).toEqual([]);
    expect(libraryTwins(undefined, candidates[4])).toEqual([]);
  });

  it("没勾上的候选不说;库里的技能还没读到时不认同名", () => {
    expect(unpickedMentions(s, candidates, new Set(), candidates[0])).toEqual([]);
    expect(unpickedMentions(undefined, candidates, new Set(["a"]), candidates[0])).toEqual(["b", "c", "lib"]);
  });
});
