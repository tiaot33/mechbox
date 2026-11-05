// 关系图的两种排法：全图每次排出一样的位置、有关系的靠得更近;点了技能时提到它的在左、它提到的在右、互相提到的在上。
import { describe, expect, it } from "vitest";
import { SourceType, type Collection, type Mention, type Skill } from "@bindings/internal/library";
import type { Snapshot } from "./library";
import { mentionGraph } from "./graph";
import { focusLayout, forceLayout } from "./graph-layout";

const skill = (id: number, name: string) => ({ id, name, description: "", source: { type: SourceType.SourceGitHub, label: "obra/superpowers" } }) as Skill;
const mention = (from: number, to: number): Mention => ({ from, to, lines: [] });
const names = ["entry", "plans", "tdd", "debug", "review", "far-a", "far-b"];
const s = {
  collections: [{ id: 1, skills: names.map((_, i) => i + 1), source: { type: SourceType.SourceGitHub, label: "obra/superpowers" } } as Collection],
  groups: [],
  skills: names.map((n, i) => skill(i + 1, n)),
  info: { home_dir: "/home" },
} as unknown as Snapshot;
const ids = names.map((_, i) => i + 1);
const edges = [mention(1, 2), mention(2, 1), mention(1, 3), mention(4, 1), mention(2, 5), mention(6, 7)];

describe("forceLayout", () => {
  it("同一张图每次排出一样的位置", () => {
    const g = mentionGraph(s, ids, edges);
    expect([...forceLayout(g)]).toEqual([...forceLayout(mentionGraph(s, ids, edges))]);
  });

  it("有关系的技能比没关系的靠得近", () => {
    const at = forceLayout(mentionGraph(s, ids, edges));
    const dist = (a: number, b: number) => Math.hypot(at.get(a)!.x - at.get(b)!.x, at.get(a)!.y - at.get(b)!.y);
    expect(dist(1, 2)).toBeLessThan(dist(1, 7));
    expect(dist(6, 7)).toBeLessThan(dist(6, 1));
  });
});

describe("focusLayout", () => {
  it("只排它和上下游：提到它的在左、它提到的在右、互相提到的在上", () => {
    const { at, relation } = focusLayout(mentionGraph(s, ids, edges), 1);
    expect([...at.keys()].sort()).toEqual([1, 2, 3, 4]);
    expect(at.get(1)).toEqual({ x: 0, y: 0 });
    expect(relation.get(4)).toBe("in");
    expect(at.get(4)!.x).toBeLessThan(0);
    expect(relation.get(3)).toBe("out");
    expect(at.get(3)!.x).toBeGreaterThan(0);
    expect(relation.get(2)).toBe("both");
    expect(at.get(2)!.y).toBeLessThan(0);
  });

  it("一列放不下时往外再加一列", () => {
    const many = Array.from({ length: 6 }, (_, i) => mention(i + 2, 1));
    const big = { ...s, skills: Array.from({ length: 7 }, (_, i) => skill(i + 1, `s${i + 1}`)) } as unknown as Snapshot;
    const { at } = focusLayout(mentionGraph(big, [1, 2, 3, 4, 5, 6, 7], many), 1, 4);
    const xs = new Set([2, 3, 4, 5, 6, 7].map((id) => at.get(id)!.x));
    expect(xs.size).toBe(2);
  });
});
