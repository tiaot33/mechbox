import { describe, expect, it } from "vitest";
import { splitLead } from "./lead";

describe("splitLead", () => {
  it("中文按第一个句号拆", () => {
    expect(splitLead("文件夹被改过。重新安装会换掉这些改动。")).toEqual(["文件夹被改过。", "重新安装会换掉这些改动。"]);
  });

  it("英文按句末标点后的空白拆", () => {
    expect(splitLead("The folder was edited. Reinstalling replaces it.")).toEqual(["The folder was edited.", " Reinstalling replaces it."]);
  });

  it("路径和版本号里的点不算句末", () => {
    expect(splitLead("Cannot read ~/.claude/skills/a.md")).toEqual(["Cannot read ~/.claude/skills/a.md", ""]);
  });

  it("换行处拆开，换行留给后面", () => {
    expect(splitLead("入库失败\nnetwork error")).toEqual(["入库失败", "\nnetwork error"]);
  });

  it("只有一句时整句是首句", () => {
    expect(splitLead("没有可入库的技能")).toEqual(["没有可入库的技能", ""]);
  });
});
