import { describe, expect, it } from "vitest";
import { messages } from "./i18n";
import { resolveFetchInput, resolveLocalPath, wslLocation } from "./paths";

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
    expect(resolveLocalPath("~/skills/a", wslHome)).toEqual({ path: wslHome + "\\skills\\a" });
    expect(resolveLocalPath("~/code/proj-a", windowsHome)).toEqual({ path: windowsHome + "\\code\\proj-a" });
    expect(resolveLocalPath("~", wslHome)).toEqual({ path: wslHome });
    expect(resolveLocalPath("~/skills", "/Users/me")).toEqual({ path: "/Users/me/skills" });
  });

  it("全局目录选了发行版时，/ 开头的路径当作那个发行版里的路径", () => {
    expect(resolveLocalPath("/home/me/proj", wslHome)).toEqual({ path: "\\\\wsl.localhost\\Ubuntu\\home\\me\\proj" });
    expect(resolveLocalPath("/opt/x", "\\\\wsl$\\Debian\\home\\me")).toEqual({ path: "\\\\wsl$\\Debian\\opt\\x" });
  });

  it("全局目录在 Windows 本机时，/ 开头的路径拦下，不猜发行版", () => {
    expect(resolveLocalPath("/home/me/proj", windowsHome)).toEqual({ error: messages().paths.linuxOnWindows });
  });

  it("Windows 路径、\\\\wsl.localhost 路径原样;其他平台上 / 开头的是本机路径", () => {
    expect(resolveLocalPath("C:\\code\\skills", wslHome)).toEqual({ path: "C:\\code\\skills" });
    expect(resolveLocalPath("\\\\wsl.localhost\\Debian\\x", windowsHome)).toEqual({ path: "\\\\wsl.localhost\\Debian\\x" });
    expect(resolveLocalPath("//wsl.localhost/Debian/x", windowsHome)).toEqual({ path: "//wsl.localhost/Debian/x" });
    expect(resolveLocalPath("/Users/me/skills", "/Users/me")).toEqual({ path: "/Users/me/skills" });
  });
});

describe("resolveFetchInput", () => {
  it("/ 开头的路径按全局目录所在的地方处理", () => {
    expect(resolveFetchInput("/home/me/skills", wslHome)).toEqual({ path: "\\\\wsl.localhost\\Ubuntu\\home\\me\\skills" });
    expect(resolveFetchInput("/home/me/skills", windowsHome)).toEqual({ error: messages().paths.linuxOnWindows });
    expect(resolveFetchInput("/Users/me/skills", "/Users/me")).toEqual({ path: "/Users/me/skills" });
  });

  it("链接与简写原样", () => {
    expect(resolveFetchInput("owner/repo", wslHome)).toEqual({ path: "owner/repo" });
    expect(resolveFetchInput("https://github.com/o/r", windowsHome)).toEqual({ path: "https://github.com/o/r" });
  });
});
