// 本地路径输入：技能库要求绝对路径(相对路径会按进程当前目录解析，从 Finder 启动时是 /)。
// 界面支持 ~ 展开，相对路径直接拦下。

export type PathInput = { path: string; error?: undefined } | { path?: undefined; error: string };

export function resolveLocalPath(input: string, home: string): PathInput | null {
  const text = input.trim();
  if (!text) return null;
  if (text === "~") return { path: home };
  if (text.startsWith("~/")) return { path: home + text.slice(1) };
  if (text.startsWith("/")) return { path: text };
  return { error: "请输入绝对路径(以 / 或 ~/ 开头),或点「选择…」" };
}
