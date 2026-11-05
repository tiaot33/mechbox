// 技能文件内容的展示辅助：Go 的 []byte 经 JSON 成为 base64 字符串。
import { messages } from "./i18n";

// decodeContent 把 base64 内容解成文本;不是合法 UTF-8(二进制文件)时返回 null。
export function decodeContent(base64: string | null): string | null {
  if (!base64) return "";
  const bytes = Uint8Array.from(atob(base64), (c) => c.charCodeAt(0));
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch {
    return null;
  }
}

export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

export function formatDate(value: string | undefined): string {
  if (!value) return messages().common.unknown;
  const d = new Date(value);
  if (Number.isNaN(d.getTime()) || d.getFullYear() < 2000) return messages().common.unknown;
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}
