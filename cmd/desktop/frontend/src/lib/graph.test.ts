// 关系图画哪些技能：只画有连线的，本集合或组以外的标出所在集合;悬停时高亮哪些。
import { describe, expect, it } from "vitest";
import { SourceType, type Collection, type Mention, type Skill } from "@bindings/internal/library";
import type { Snapshot } from "./library";
import { edgeKey, mentionGraph, neighbours } from "./graph";

const skill = (id: number, name: string, collection_id = 1) => ({ id, name, collection_id }) as Skill;
const collection = (id: number, label: string, skills: number[]) => ({ id, skills, source: { type: SourceType.SourceGitHub, label } }) as Collection;
const mention = (from: number, to: number): Mention => ({ from, to, lines: [] });
const snapshot = (s: Partial<Snapshot>) => ({ collections: [], groups: [], skills: [], info: { home_dir: "/home" }, ...s }) as unknown as Snapshot;

describe("mentionGraph", () => {
  const s = snapshot({
    skills: [skill(1, "entry"), skill(2, "writing-plans"), skill(3, "loner"), skill(4, "shared-tool", 2), skill(5, "orphan", 0)],
    collections: [collection(1, "obra/superpowers", [1, 2, 3]), collection(2, "acme/tools", [4])],
  });

  it("只画有连线的技能，没有连线的收在下面", () => {
    const g = mentionGraph(s, [1, 2, 3], [mention(1, 2)]);
    expect(g.nodes.map((n) => n.skill.name)).toEqual(["entry", "writing-plans"]);
    expect(g.loose.map((x) => x.name)).toEqual(["loner"]);
  });

  it("提到的库里其他集合的技能也画进来，标出所在集合;没有来源的写 null", () => {
    const g = mentionGraph(s, [1, 2, 3], [mention(1, 4), mention(2, 5)]);
    expect(g.nodes.map((n) => [n.skill.name, n.outside, n.where])).toEqual([
      ["entry", false, undefined],
      ["orphan", true, null],
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

describe("neighbours", () => {
  it("高亮它提到的、提到它的和相连的线", () => {
    const edges = [mention(1, 2), mention(3, 1), mention(2, 4)];
    const n = neighbours(edges, 1);
    expect([...n.nodes].sort()).toEqual([1, 2, 3]);
    expect([...n.edges].sort()).toEqual([edgeKey(mention(1, 2)), edgeKey(mention(3, 1))].sort());
  });
});
