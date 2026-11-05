import { describe, expect, it } from "vitest";
import { linkTarget } from "./markdown";

const files = new Set(["SKILL.md", "references/flowcharts.md", "references/a b.md", "scripts/run.sh"]);

describe("linkTarget", () => {
  it("外部网址交给系统浏览器", () => {
    expect(linkTarget("https://example.com/x", "SKILL.md", files)).toEqual({ url: "https://example.com/x" });
    expect(linkTarget("HTTP://example.com", undefined, undefined)).toEqual({ url: "HTTP://example.com" });
    expect(linkTarget("mailto:a@example.com")).toEqual({ url: "mailto:a@example.com" });
  });

  it("相对路径按当前文件所在目录解析到技能里的文件", () => {
    expect(linkTarget("references/flowcharts.md", "SKILL.md", files)).toEqual({ file: "references/flowcharts.md" });
    expect(linkTarget("./flowcharts.md#top", "references/a b.md", files)).toEqual({ file: "references/flowcharts.md" });
    expect(linkTarget("../SKILL.md", "references/flowcharts.md", files)).toEqual({ file: "SKILL.md" });
    expect(linkTarget("references/a%20b.md", "SKILL.md", files)).toEqual({ file: "references/a b.md" });
  });

  it("其余的不当链接", () => {
    expect(linkTarget(undefined, "SKILL.md", files)).toBeNull();
    expect(linkTarget("#usage", "SKILL.md", files)).toBeNull();
    expect(linkTarget("missing.md", "SKILL.md", files)).toBeNull();
    expect(linkTarget("../outside.md", "SKILL.md", files)).toBeNull();
    expect(linkTarget("/etc/passwd", "SKILL.md", files)).toBeNull();
    expect(linkTarget("javascript:alert(1)", "SKILL.md", files)).toBeNull();
    expect(linkTarget("file:///etc/passwd", "SKILL.md", files)).toBeNull();
    expect(linkTarget("SKILL.md", undefined, undefined)).toBeNull();
    expect(linkTarget("%E0%A4%A", "SKILL.md", files)).toBeNull();
  });
});
