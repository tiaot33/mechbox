// 本地路径输入：技能库要求绝对路径(相对路径会按进程当前目录解析，从 Finder 启动时是 /)。
// 界面支持 ~ 展开，相对路径直接拦下。绝对路径包括 Windows 的盘符路径与 \\wsl.localhost\... 这类网络路径。
// Windows 上 / 开头的是 WSL 发行版里的 Linux 路径：全局目录选了发行版时当作那个发行版里的路径，否则拦下(不猜是哪个发行版)。
import { messages } from "./i18n";

const absolute = /^(\/|[A-Za-z]:[\\/]|\\\\)/;
const wslPrefix = /^(?:\\\\|\/\/)wsl(?:\.localhost|\$)[\\/]([^\\/]+)/i;

export type PathInput = { path: string; error?: undefined } | { path?: undefined; error: string };

// home 是全局目录所在的主目录：Windows 上是 C:\Users\... 或 \\wsl.localhost\<发行版>\home\...。
export function resolveLocalPath(input: string, home: string): PathInput | null {
  const text = input.trim();
  if (!text) return null;
  const distroRoot = wslPrefix.exec(home)?.[0];
  if (text === "~") return { path: home };
  if (text.startsWith("~/") || text.startsWith("~\\")) {
    return { path: home + (onWindows(home) ? text.slice(1).replaceAll("/", "\\") : text.slice(1)) };
  }
  if (onWindows(home) && /^\/(?!\/)/.test(text)) {
    if (!distroRoot) return { error: messages().paths.linuxOnWindows };
    return { path: distroRoot + text.replaceAll("/", "\\") };
  }
  if (absolute.test(text)) return { path: text };
  return { error: messages().paths.absolute };
}

// onWindows 按主目录判断是不是在 Windows 上:Windows 本机的主目录带盘符，选了 WSL 发行版时是 \\wsl.localhost\...。
function onWindows(home: string): boolean {
  return /^[A-Za-z]:/.test(home) || isWSLPath(home);
}

// isLocalPath 判断(~ 已展开的)入库输入是不是本地绝对路径。
export function isLocalPath(path: string): boolean {
  return absolute.test(path.trim());
}

// insideHome 判断 path 是否在主目录 home 里面(不含主目录本身);Windows 与 WSL 发行版的路径用反斜杠分隔。
export function insideHome(path: string, home: string): boolean {
  const base = home.replace(/[\\/]+$/, "");
  const rest = path.startsWith(base + "/") || path.startsWith(base + "\\") ? path.slice(base.length) : "";
  return rest.replace(/[\\/]+/g, "") !== "";
}

// isWSLPath 判断 path 是否在某个 WSL 发行版里(\\wsl.localhost\... 或 \\wsl$\...)。
export function isWSLPath(path: string): boolean {
  return wslPrefix.test(path);
}

// wslLocation 把 WSL 发行版里的路径拆成发行版名字和发行版里的 Linux 路径(/ 分隔，如 /home/me/proj);不在发行版里时为 null。
export function wslLocation(path: string): { distro: string; path: string } | null {
  const m = wslPrefix.exec(path);
  if (!m) return null;
  const rest = path.slice(m[0].length).replace(/[\\/]+/g, "/").replace(/\/$/, "");
  return { distro: m[1], path: rest || "/" };
}

// resolveFetchInput 处理入库输入框的内容：以 ~ 开头的展开成主目录下的绝对路径;Windows 上 / 开头的 Linux 路径按
// resolveLocalPath 的规则处理;以 ./、../ 开头的相对路径拦下(技能库按进程当前目录解析);
// 其他输入(绝对路径、GitHub 简写、各种链接)原样交给技能库识别。
export function resolveFetchInput(input: string, home: string): PathInput | null {
  const text = input.trim();
  if (!text) return null;
  if (text === "~" || text.startsWith("~/") || text.startsWith("~\\") || /^\/(?!\/)/.test(text)) return resolveLocalPath(text, home);
  if (/^\.\.?(\/|$)/.test(text)) return { error: messages().paths.fetchRelative };
  return { path: text };
}
