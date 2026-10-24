// 本地路径输入：技能库要求绝对路径(相对路径会按进程当前目录解析，从 Finder 启动时是 /)。
// 界面支持 ~ 展开，相对路径直接拦下。绝对路径包括 Windows 的盘符路径与 \\wsl.localhost\... 这类网络路径。
import { messages } from "./i18n";

const absolute = /^(\/|[A-Za-z]:[\\/]|\\\\)/;

export type PathInput = { path: string; error?: undefined } | { path?: undefined; error: string };

export function resolveLocalPath(input: string, home: string): PathInput | null {
  const text = input.trim();
  if (!text) return null;
  if (text === "~") return { path: home };
  if (text.startsWith("~/") || text.startsWith("~\\")) return { path: home + text.slice(1) };
  if (absolute.test(text)) return { path: text };
  return { error: messages().paths.absolute };
}

// insideHome 判断 path 是否在主目录 home 里面(不含主目录本身);Windows 与 WSL 发行版的路径用反斜杠分隔。
export function insideHome(path: string, home: string): boolean {
  const base = home.replace(/[\\/]+$/, "");
  const rest = path.startsWith(base + "/") || path.startsWith(base + "\\") ? path.slice(base.length) : "";
  return rest.replace(/[\\/]+/g, "") !== "";
}

// isWSLPath 判断 path 是否在某个 WSL 发行版里(\\wsl.localhost\... 或 \\wsl$\...)。
export function isWSLPath(path: string): boolean {
  return /^(\\\\|\/\/)wsl(\.localhost|\$)[\\/]/i.test(path);
}

// resolveFetchInput 处理入库输入框的内容：以 ~ 开头的展开成主目录下的绝对路径;以 ./、../ 开头的相对路径拦下
// (技能库按进程当前目录解析);其他输入(绝对路径、GitHub 简写、各种链接)原样交给技能库识别。
export function resolveFetchInput(input: string, home: string): PathInput | null {
  const text = input.trim();
  if (!text) return null;
  if (text === "~" || text.startsWith("~/") || text.startsWith("~\\")) return resolveLocalPath(text, home);
  if (/^\.\.?(\/|$)/.test(text)) return { error: messages().paths.fetchRelative };
  return { path: text };
}
