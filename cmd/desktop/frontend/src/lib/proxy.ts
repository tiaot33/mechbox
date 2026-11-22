import type { SystemProxy } from "@bindings/internal/library";

// formatSystemProxy 把读到的系统静态代理整理成单行展示文案;一项都没有时返回空字符串。
export function formatSystemProxy(sys?: SystemProxy): string {
  if (!sys) return "";
  const http = sys.http?.trim() ?? "";
  const https = sys.https?.trim() ?? "";
  const socks = sys.socks?.trim() ?? "";
  const parts: string[] = [];
  if (http && https && http === https) {
    parts.push(http);
  } else {
    if (http) parts.push(`HTTP ${http}`);
    if (https) parts.push(`HTTPS ${https}`);
  }
  if (socks) parts.push(socks);
  return parts.join(" · ");
}
