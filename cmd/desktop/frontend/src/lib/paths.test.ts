import { describe, expect, it } from "vitest";
import { messages } from "./i18n";
import { insideHome, resolveFetchInput, resolveLocalPath, wslLocation } from "./paths";

const wslHome = "\\\\wsl.localhost\\Ubuntu\\home\\me";
const windowsHome = "C:\\Users\\me";

describe("wslLocation", () => {
  it("拆出发行版和 Linux 路径", () => {
    expect(wslLocation("\\\\wsl.localhost\\Ubuntu\\home\\me\\proj")).toEqual({ distro: "Ubuntu", path: "/home/me/proj" });
    expect(wslLocation("\\\\wsl$\\Debian\\opt\\x\\")).toEqual({ distro: "Debian", path: "/opt/x" });
    expect(wslLocation("//wsl.localhost/Ubuntu/home/me")).toEqual({ distro: "Ubuntu", path: "/home/me" });
    expect(wslLocation("\\\\wsl.localhost\\Ubuntu")).toEqual({ distro: "Ubuntu", path: "/" });
  });

  it("不在发行版里时为 null", () => {
    expect(wslLocation("C:\\Users\\me")).toBeNull();
    expect(wslLocation("/Users/me")).toBeNull();
    expect(wslLocation("\\\\server\\share")).toBeNull();
  });
});

describe("resolveLocalPath", () => {
  it("~/ 展开成全局目录所在的主目录，Windows 上(含发行版里)用反斜杠", () => {
    expect(resolveLocalPath("~/skills/a", wslHome, true)).toEqual({ path: wslHome + "\\skills\\a" });
    expect(resolveLocalPath("~/code/proj-a", windowsHome, true)).toEqual({ path: windowsHome + "\\code\\proj-a" });
    expect(resolveLocalPath("~/code/proj-a", "\\\\server\\users\\me", false)).toEqual({ path: "\\\\server\\users\\me\\code\\proj-a" });
    expect(resolveLocalPath("~", wslHome, true)).toEqual({ path: wslHome });
    expect(resolveLocalPath("~/skills", "/Users/me", false)).toEqual({ path: "/Users/me/skills" });
  });

  it("全局目录选了发行版时，/ 开头的路径当作那个发行版里的路径", () => {
    expect(resolveLocalPath("/home/me/proj", wslHome, true)).toEqual({ path: "\\\\wsl.localhost\\Ubuntu\\home\\me\\proj" });
    expect(resolveLocalPath("/opt/x", "\\\\wsl$\\Debian\\home\\me", true)).toEqual({ path: "\\\\wsl$\\Debian\\opt\\x" });
  });

  it("装了发行版、全局目录在 Windows 本机时，/ 开头的路径拦下，不猜发行版(主目录是网络路径时也一样)", () => {
    expect(resolveLocalPath("/home/me/proj", windowsHome, true)).toEqual({ error: messages().paths.linuxOnWindows });
    expect(resolveLocalPath("/home/me/proj", "\\\\server\\users\\me", true)).toEqual({ error: messages().paths.linuxOnWindows });
  });

  it("没装发行版时 / 开头的路径照原样交给技能库", () => {
    expect(resolveLocalPath("/Users/me/code/proj", windowsHome, false)).toEqual({ path: "/Users/me/code/proj" });
    expect(resolveLocalPath("/Users/me/skills", "/Users/me", false)).toEqual({ path: "/Users/me/skills" });
  });

  it("Windows 路径、\\\\wsl.localhost 路径原样", () => {
    expect(resolveLocalPath("C:\\code\\skills", wslHome, true)).toEqual({ path: "C:\\code\\skills" });
    expect(resolveLocalPath("\\\\wsl.localhost\\Debian\\x", windowsHome, true)).toEqual({ path: "\\\\wsl.localhost\\Debian\\x" });
    expect(resolveLocalPath("//wsl.localhost/Debian/x", windowsHome, true)).toEqual({ path: "//wsl.localhost/Debian/x" });
  });
});

describe("resolveFetchInput", () => {
  it("/ 开头的路径按全局目录所在的地方处理", () => {
    expect(resolveFetchInput("/home/me/skills", wslHome, true)).toEqual({ path: "\\\\wsl.localhost\\Ubuntu\\home\\me\\skills" });
    expect(resolveFetchInput("/home/me/skills", windowsHome, true)).toEqual({ error: messages().paths.linuxOnWindows });
    expect(resolveFetchInput("/home/me/skills", windowsHome, false)).toEqual({ path: "/home/me/skills" });
    expect(resolveFetchInput("/Users/me/skills", "/Users/me", false)).toEqual({ path: "/Users/me/skills" });
  });

  it("链接与简写原样", () => {
    expect(resolveFetchInput("owner/repo", wslHome, true)).toEqual({ path: "owner/repo" });
    expect(resolveFetchInput("https://github.com/o/r", windowsHome, true)).toEqual({ path: "https://github.com/o/r" });
  });
});

describe("insideHome", () => {
  it("发行版里的路径不分 \\\\wsl$ 与 \\\\wsl.localhost、发行版名不分大小写", () => {
    expect(insideHome("\\\\wsl$\\Ubuntu\\home\\me\\.x\\skills", wslHome)).toBe(true);
    expect(insideHome("\\\\wsl.localhost\\ubuntu\\home\\me\\.x\\skills", wslHome)).toBe(true);
    expect(insideHome("\\\\wsl.localhost\\Ubuntu\\home\\me", wslHome)).toBe(false);
    expect(insideHome("\\\\wsl.localhost\\Ubuntu\\home\\melody\\x", wslHome)).toBe(false);
    expect(insideHome("\\\\wsl.localhost\\Debian\\home\\me\\x", wslHome)).toBe(false);
  });

  it("其他路径按前缀比较", () => {
    expect(insideHome("C:\\Users\\me\\.x\\skills", windowsHome)).toBe(true);
    expect(insideHome("C:\\Users\\me", windowsHome)).toBe(false);
    expect(insideHome("/Users/me/.x", "/Users/me")).toBe(true);
  });
});
