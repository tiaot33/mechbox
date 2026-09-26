// 技能文件内容的展示辅助：Go 的 []byte 经 JSON 成为 base64 字符串。

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
  if (!value) return "未知";
  const d = new Date(value);
  if (Number.isNaN(d.getTime()) || d.getFullYear() < 2000) return "未知";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

const escapeHTML = (s: string) =>
  s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]!);

// renderMarkdown 是原型里的极简渲染：标题、列表、段落、行内代码与粗体;先转义再套标签，不执行任何 HTML。
export function renderMarkdown(md: string): string {
  md = md.replace(/^---\n[\s\S]*?\n---\n/, "");
  const inline = (s: string) =>
    escapeHTML(s)
      .replace(/`([^`]+)`/g, "<code>$1</code>")
      .replace(/\*\*([^*]+)\*\*/g, "<b>$1</b>");
  let html = "";
  let list: "ul" | "ol" | null = null;
  let fence = false;
  const close = () => {
    if (list) html += `</${list}>`;
    list = null;
  };
  for (const line of md.split("\n")) {
    if (line.startsWith("```")) {
      close();
      html += fence ? "</code></pre>" : "<pre><code>";
      fence = !fence;
      continue;
    }
    if (fence) {
      html += escapeHTML(line) + "\n";
      continue;
    }
    let m: RegExpMatchArray | null;
    if ((m = line.match(/^(#{1,3}) (.*)/))) {
      close();
      const level = m[1].length + 2;
      html += `<h${level}>${inline(m[2])}</h${level}>`;
    } else if ((m = line.match(/^\s*[-*] (.*)/))) {
      if (list !== "ul") {
        close();
        html += "<ul>";
        list = "ul";
      }
      html += `<li>${inline(m[1])}</li>`;
    } else if ((m = line.match(/^\s*\d+\. (.*)/))) {
      if (list !== "ol") {
        close();
        html += "<ol>";
        list = "ol";
      }
      html += `<li>${inline(m[1])}</li>`;
    } else if (line.trim()) {
      close();
      html += `<p>${inline(line)}</p>`;
    } else close();
  }
  close();
  if (fence) html += "</code></pre>";
  return html;
}
