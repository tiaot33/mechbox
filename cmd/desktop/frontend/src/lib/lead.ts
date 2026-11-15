// splitLead 把一段提示拆成首句和其余部分：首句到第一个句末标点(。！？；或英文的 . ! ? 后跟空白)或换行为止。
// 只有一句时其余部分为空。
export function splitLead(text: string): [string, string] {
  const m = /[。！？；]|[.!?](?=\s)|\n/.exec(text);
  if (!m) return [text, ""];
  const end = m[0] === "\n" ? m.index : m.index + 1;
  return [text.slice(0, end), text.slice(end)];
}
