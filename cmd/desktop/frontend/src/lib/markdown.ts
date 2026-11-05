// 渲染出来的 Markdown 里链接的去向:Wails 窗口里点了真链接会把整个应用导航走，所以每个链接都先在这里判断。

export type LinkTarget = { url: string } | { file: string } | null;

// linkTarget 判断链接点了去哪里:http(s) 与 mailto 交给系统浏览器;from 是正在看的文件，
// 相对路径按它所在的目录解析，指到 files 里的文件时切换到那个文件;其余(页内锚点、别的协议、技能外的路径)不当链接。
export function linkTarget(href: string | undefined, from?: string, files?: ReadonlySet<string>): LinkTarget {
  if (!href) return null;
  if (/^(https?:\/\/|mailto:)/i.test(href)) return { url: browserURL(href) };
  if (from === undefined || !files || /^[a-z][a-z0-9+.-]*:/i.test(href) || /^[/#\\]/.test(href)) return null;
  let rel = href.replace(/[?#].*$/, "");
  try {
    rel = decodeURIComponent(rel);
  } catch {
    return null;
  }
  const parts = from.split("/").slice(0, -1);
  for (const part of rel.split("/")) {
    if (part === "" || part === ".") continue;
    if (part === "..") {
      if (!parts.length) return null;
      parts.pop();
    } else parts.push(part);
  }
  const path = parts.join("/");
  return files.has(path) ? { file: path } : null;
}

// browserURL 把 Wails 的 Browser.OpenURL 会拒绝的字符(它当成 shell 元字符的 ; | ` $ \ < > 与空白)写成百分号编码，
// 如 OData 的 ?$top=5、带 ;version=2 的路径;编码后服务端照常解得出来。
function browserURL(url: string): string {
  return url.replace(/[;|`$\\<>\s]/g, (c) => "%" + c.charCodeAt(0).toString(16).toUpperCase().padStart(2, "0"));
}
