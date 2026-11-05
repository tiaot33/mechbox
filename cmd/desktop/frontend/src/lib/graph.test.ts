// 关系图画哪些技能：只画有连线的，本集合或组以外的标出所在集合;互相提到合成一条线;圆点按被提到的次数;
// 悬停与点技能时留哪些;哪些写出名字;什么变了才重新排;搜索数几个;提示里的原文怎样截。
import { describe, expect, it } from "vitest";
import { SourceType, type Collection, type Mention, type Skill } from "@bindings/internal/library";
import type { Snapshot } from "./library";
import { labelled, layoutKey, mentionGraph, mentionSnippet, neighbours, relations, searchHits } from "./graph";

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
    expect(g.nodes.map((n) => [n.skill.name, n.outside, n.where, n.collection])).toEqual([
      ["entry", false, undefined, 1],
      ["orphan", true, "没有来源", null],
      ["shared-tool", true, "acme/tools", 2],
      ["writing-plans", false, undefined, 1],
    ]);
  });

  it("互相提到只画一条线，两头都有箭头;圆点大小按被几个技能提到", () => {
    const g = mentionGraph(s, [1, 2, 3], [mention(1, 2), mention(2, 1), mention(3, 2)]);
    expect(g.links.map((l) => [l.from, l.to, l.both, l.mentions.length])).toEqual([
      [1, 2, true, 2],
      [3, 2, false, 1],
    ]);
    expect(g.nodes.map((n) => [n.skill.name, n.mentioned])).toEqual([
      ["entry", 1],
      ["loner", 0],
      ["writing-plans", 2],
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
  it("虚线圆的技能和图下方没有连线的技能都算", () => {
    const g = mentionGraph(s, [1, 2, 3], [mention(1, 4)]);
    expect(searchHits(g, "frontend")).toBe(1);
    expect(searchHits(g, "loner")).toBe(1);
    expect(searchHits(g, "")).toBe(4);
  });
});

describe("neighbours", () => {
  it("高亮它提到的、提到它的和相连的线", () => {
    const g = mentionGraph(s, [1, 2, 3], [mention(1, 2), mention(3, 1), mention(2, 4)]);
    const n = neighbours(g.links, 1);
    expect([...n.nodes].sort()).toEqual([1, 2, 3]);
    expect([...n.links].sort()).toEqual(["1-2", "1-3"]);
  });
});

describe("relations", () => {
  it("点了一个技能：提到它的、它提到的、互相提到的分开", () => {
    const g = mentionGraph(s, [1, 2, 3], [mention(1, 2), mention(2, 1), mention(3, 1), mention(1, 4)]);
    expect([...relations(g.links, 1)].sort()).toEqual([
      [2, "both"],
      [3, "in"],
      [4, "out"],
    ]);
  });
});

describe("labelled", () => {
  const nodes = (counts: number[]) => counts.map((mentioned, i) => ({ skill: skill(i + 1, `s${i + 1}`), outside: false, collection: 1, mentioned }));

  it("不超过 30 个技能时全部写出名字", () => {
    expect(labelled(nodes(Array(30).fill(0))).size).toBe(30);
  });

  it("更多时只写被提到最多的约 15 个，和第 15 个一样多的也写", () => {
    const counts = Array.from({ length: 40 }, (_, i) => (i < 14 ? 10 : i < 17 ? 3 : 0));
    const shown = labelled(nodes(counts));
    expect(shown.size).toBe(17);
    expect(shown.has(18)).toBe(false);
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
