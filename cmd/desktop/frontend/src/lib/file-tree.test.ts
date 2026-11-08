import { describe, expect, it } from "vitest";
import { fileTree } from "./file-tree";

describe("fileTree", () => {
  it("同一层目录在前、文件在后，各自按名字排;文件带着原始数据", () => {
    const tree = fileTree([{ path: "SKILL.md" }, { path: "scripts/run.sh" }, { path: "agents/openai.yaml" }, { path: "LICENSE.txt" }]);
    expect(tree.map((n) => n.name)).toEqual(["agents", "scripts", "LICENSE.txt", "SKILL.md"]);
    expect(tree[1]).toEqual({
      name: "scripts",
      path: "scripts",
      children: [
        {
          name: "run.sh",
          path: "scripts/run.sh",
          item: { path: "scripts/run.sh" },
          children: [],
        },
      ],
    });
  });
});
