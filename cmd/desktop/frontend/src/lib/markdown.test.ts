import { describe, expect, it } from "vitest";
import { linkTarget } from "./markdown";

const files = new Set(["SKILL.md", "references/flowcharts.md", "references/a b.md", "scripts/run.sh"]);

describe("linkTarget", () => {
  it("外部网址交给系统浏览器", () => {
    expect(linkTarget("https://example.com/x", "SKILL.md", files)).toEqual({ url: "https://example.com/x" });
    expect(linkTarget("HTTP://example.com", undefined, undefined)).toEqual({ url: "HTTP://example.com" });
    expect(linkTarget("mailto:a@example.com")).toEqual({ url: "mailto:a@example.com" });
  });

  it("外部网址里 Wails 当成 shell 元字符拒绝的字符写成百分号编码", () => {
    expect(linkTarget("https://example.com/odata/People?$top=5")).toEqual({ url: "https://example.com/odata/People?%24top=5" });
    expect(linkTarget("https://example.com/docs;version=2")).toEqual({ url: "https://example.com/docs%3Bversion=2" });
    expect(linkTarget("https://example.com/a|b`c\\d<e>")).toEqual({ url: "https://example.com/a%7Cb%60c%5Cd%3Ce%3E" });
    expect(linkTarget("https://example.com/?q=%24")).toEqual({ url: "https://example.com/?q=%24" });
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
