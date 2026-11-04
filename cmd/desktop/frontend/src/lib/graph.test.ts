// 关系图画哪些技能：只画有连线的，本集合或组以外的标出所在集合;悬停时高亮哪些;什么变了才重新排;搜索数几个;提示里的原文怎样截。
import { describe, expect, it } from "vitest";
import { SourceType, type Collection, type Mention, type Skill } from "@bindings/internal/library";
import type { Snapshot } from "./library";
import { edgeKey, layoutKey, mentionGraph, mentionSnippet, neighbours, searchHits } from "./graph";

const github = (label: string) => ({ type: SourceType.SourceGitHub, label });
const skill = (id: number, name: string, label = "obra/superpowers", description = "") =>
  ({ id, name, description, source: label ? github(label) : { type: SourceType.$zero, label: "" } }) as Skill;
const collection = (id: number, label: string, skills: number[]) => ({ id, skills, source: github(label) }) as Collection;
const mention = (from: number, to: number): Mention => ({ from, to, lines: [] });
const snapshot = (s: Partial<Snapshot>) => ({ collections: [], groups: [], skills: [], info: { home_dir: "/home" }, ...s }) as unknown as Snapshot;

const s = snapshot({
  skills: [skill(1, "entry"), skill(2, "writing-plans"), skill(3, "loner"), skill(4, "shared-tool", "acme/tools", "frontend helpers"), skill(5, "orphan", "")],
  collections: [collection(1, "obra/superpowers", [1, 2, 3]), collection(2, "acme/tools", [4])],
});

describe("mentionGraph", () => {
  it("只画有连线的技能，没有连线的收在下面", () => {
    const g = mentionGraph(s, [1, 2, 3], [mention(1, 2)]);
    expect(g.nodes.map((n) => n.skill.name)).toEqual(["entry", "writing-plans"]);
    expect(g.loose.map((x) => x.name)).toEqual(["loner"]);
  });

  it("提到的本集合或组以外的技能也画进来，标出所在集合;没有来源的写没有来源", () => {
    const g = mentionGraph(s, [1, 2, 3], [mention(1, 4), mention(2, 5)]);
    expect(g.nodes.map((n) => [n.skill.name, n.outside, n.where])).toEqual([
      ["entry", false, undefined],
      ["orphan", true, "没有来源"],
      ["shared-tool", true, "acme/tools"],
      ["writing-plans", false, undefined],
    ]);
  });

  it("丢掉库里已经没有的技能和不从这组技能出发的线", () => {
    const g = mentionGraph(s, [1, 2, 3], [mention(1, 99), mention(4, 1)]);
    expect(g.edges).toEqual([]);
    expect(g.nodes).toEqual([]);
    expect(g.loose.map((x) => x.name)).toEqual(["entry", "loner", "writing-plans"]);
  });
});

describe("layoutKey", () => {
  it("技能和线没变时不变，多一条线、改了名字就变", () => {
    const key = layoutKey(mentionGraph(s, [1, 2, 3], [mention(1, 2)]));
    const noted = snapshot({ ...s, skills: s.skills.map((x) => (x.id === 1 ? { ...x, note: "备注" } : x)) });
    expect(layoutKey(mentionGraph(noted, [1, 2, 3], [mention(1, 2)]))).toBe(key);
    expect(layoutKey(mentionGraph(s, [1, 2, 3], [mention(1, 2), mention(2, 1)]))).not.toBe(key);
    const renamed = snapshot({ ...s, skills: s.skills.map((x) => (x.id === 2 ? { ...x, name: "plans" } : x)) });
    expect(layoutKey(mentionGraph(renamed, [1, 2, 3], [mention(1, 2)]))).not.toBe(key);
  });
});

describe("searchHits", () => {
  it("虚线框里的技能和图下方没有连线的技能都算", () => {
    const g = mentionGraph(s, [1, 2, 3], [mention(1, 4)]);
    expect(searchHits(g, "frontend")).toBe(1);
    expect(searchHits(g, "loner")).toBe(1);
    expect(searchHits(g, "")).toBe(4);
  });
});

describe("neighbours", () => {
  it("高亮它提到的、提到它的和相连的线", () => {
    const edges = [mention(1, 2), mention(3, 1), mention(2, 4)];
    const n = neighbours(edges, 1);
    expect([...n.nodes].sort()).toEqual([1, 2, 3]);
    expect([...n.edges].sort()).toEqual([edgeKey(mention(1, 2)), edgeKey(mention(3, 1))].sort());
  });
});

describe("mentionSnippet", () => {
  it("标出完整出现的名字，较长的词里的不算", () => {
    expect(mentionSnippet("Use /review, not pre-review or reviewer.", "review")).toEqual({
      cut: false,
      parts: [
        { text: "Use /", hit: false },
        { text: "review", hit: true },
        { text: ", not pre-review or reviewer.", hit: false },
      ],
    });
  });

  it("长行里名字在后面时，从名字前一点的词开始截", () => {
    const text = `${"word ".repeat(60)}then use writing-plans to plan.`;
    const snip = mentionSnippet(text, "writing-plans");
    expect(snip.cut).toBe(true);
    expect(snip.parts.map((p) => p.text).join("")).toMatch(/^word .*then use writing-plans to plan\.$/);
    expect(snip.parts.map((p) => p.text).join("").length).toBeLessThan(80);
    expect(snip.parts.find((p) => p.hit)?.text).toBe("writing-plans");
  });

  it("短行不截", () => {
    expect(mentionSnippet("x".repeat(100) + " ship", "ship").cut).toBe(false);
  });
});
