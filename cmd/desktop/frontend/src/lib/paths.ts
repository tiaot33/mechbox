// 本地路径输入：技能库要求绝对路径(相对路径会按进程当前目录解析，从 Finder 启动时是 /)。
// 界面支持 ~ 展开，相对路径直接拦下。绝对路径包括 Windows 的盘符路径与 \\wsl.localhost\... 这类网络路径。
// 装了 WSL 发行版的 Windows 上，/ 开头的是发行版里的 Linux 路径：全局目录选了发行版时当作那个发行版里的路径，否则拦下(不猜是哪个发行版)。
import { messages } from "./i18n";

const absolute = /^(\/|[A-Za-z]:[\\/]|\\\\)/;
const wslPrefix = /^(?:\\\\|\/\/)wsl(?:\.localhost|\$)[\\/]([^\\/]+)/i;

export type PathInput = { path: string; error?: undefined } | { path?: undefined; error: string };

// home 是全局目录所在的主目录：Windows 上是 C:\Users\...、\\server\...,或选了发行版时的 \\wsl.localhost\<发行版>\home\...。
// wsl 是这台机器装没装 WSL 发行版(只有 Windows 上会有):没装时 / 开头的路径照原样交给技能库。
export function resolveLocalPath(input: string, home: string, wsl: boolean): PathInput | null {
  const text = input.trim();
  if (!text) return null;
  if (text === "~") return { path: home };
  if (text.startsWith("~/") || text.startsWith("~\\")) {
    return { path: home + (windowsHome(home) ? text.slice(1).replaceAll("/", "\\") : text.slice(1)) };
  }
  if (wsl && /^\/(?!\/)/.test(text)) {
    const distroRoot = wslPrefix.exec(home)?.[0];
    if (!distroRoot) return { error: messages().paths.linuxOnWindows };
    return { path: distroRoot + text.replaceAll("/", "\\") };
  }
  if (absolute.test(text)) return { path: text };
  return { error: messages().paths.absolute };
}

// windowsHome 按主目录的写法判断是不是 Windows 上的主目录：带盘符，或 \\ 开头的网络路径(含选了发行版时的 \\wsl.localhost\...)。
export function windowsHome(home: string): boolean {
  return /^([A-Za-z]:|\\\\|\/\/)/.test(home);
}

// isLocalPath 判断(~ 已展开的)入库输入是不是本地绝对路径。
export function isLocalPath(path: string): boolean {
  return absolute.test(path.trim());
}

// insideHome 判断 path 是否在主目录 home 里面(不含主目录本身);Windows 与 WSL 发行版的路径用反斜杠分隔。
// 两边都在发行版里时按 wslLocation 比较：发行版名不分大小写，\\wsl$ 与 \\wsl.localhost 算同一处。
export function insideHome(path: string, home: string): boolean {
  const rest = wslRest(path, home);
  if (rest !== null) return rest !== "" && rest !== "/";
  const base = home.replace(/[\\/]+$/, "");
  const tail = path.startsWith(base + "/") || path.startsWith(base + "\\") ? path.slice(base.length) : "";
  return tail.replace(/[\\/]+/g, "") !== "";
}

// wslRest 是 path 在发行版主目录 home 下的那一段(/ 开头，就是主目录本身时为空);两者不都在同一个发行版里或不在主目录下时为 null。
export function wslRest(path: string, home: string): string | null {
  const at = wslLocation(path);
  const base = wslLocation(home);
  if (!at || !base || at.distro.toLowerCase() !== base.distro.toLowerCase()) return null;
  if (at.path === base.path) return "";
  return at.path.startsWith(base.path + "/") ? at.path.slice(base.path.length) : null;
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

// resolveFetchInput 处理入库输入框的内容：以 ~ 开头的展开成主目录下的绝对路径;装了 WSL 发行版时 / 开头的 Linux 路径按
// resolveLocalPath 的规则处理;以 ./、../ 开头的相对路径拦下(技能库按进程当前目录解析);
// 其他输入(绝对路径、GitHub 简写、各种链接)原样交给技能库识别。
export function resolveFetchInput(input: string, home: string, wsl: boolean): PathInput | null {
  const text = input.trim();
  if (!text) return null;
  if (text === "~" || text.startsWith("~/") || text.startsWith("~\\") || (wsl && /^\/(?!\/)/.test(text))) return resolveLocalPath(text, home, wsl);
  if (/^\.\.?(\/|$)/.test(text)) return { error: messages().paths.fetchRelative };
  return { path: text };
}
