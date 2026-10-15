// 更新计划里看差异：按行比较库里的版本和来源里的新内容，没变的长段落折起来只留上下文。

export type DiffLine = { kind: "same" | "add" | "del"; text: string } | { kind: "skip"; count: number };

// 两边行数相乘超过这个数就不逐行比较，整段当作删掉再加上，免得大文件卡住界面。
const maxCells = 4_000_000;
const context = 3;

export function lineDiff(before: string, after: string): DiffLine[] {
  // 两边(没有的一侧不算)都以换行结尾时，末尾的换行只是行的结束，不显示成多出来的空行;
  // 只有一边有时照常拆开，末尾多出的空行就是这处改动。
  const trim = (!before || before.endsWith("\n")) && (!after || after.endsWith("\n"));
  const split = (text: string) => (text ? (trim ? text.slice(0, -1) : text).split("\n") : []);
  const a = split(before);
  const b = split(after);
  let head = 0;
  while (head < a.length && head < b.length && a[head] === b[head]) head++;
  let tail = 0;
  while (tail < a.length - head && tail < b.length - head && a[a.length - 1 - tail] === b[b.length - 1 - tail]) tail++;
  const midA = a.slice(head, a.length - tail);
  const midB = b.slice(head, b.length - tail);
  const lines: DiffLine[] = [
    ...a.slice(0, head).map((text) => ({ kind: "same" as const, text })),
    ...middle(midA, midB),
    ...a.slice(a.length - tail).map((text) => ({ kind: "same" as const, text })),
  ];
  return fold(lines);
}

function middle(a: string[], b: string[]): DiffLine[] {
  if (a.length * b.length > maxCells) {
    return [...a.map((text) => ({ kind: "del" as const, text })), ...b.map((text) => ({ kind: "add" as const, text }))];
  }
  // lcs[i][j] 是 a[i:] 与 b[j:] 的最长公共子序列长度。
  const width = b.length + 1;
  const lcs = new Uint32Array((a.length + 1) * width);
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      lcs[i * width + j] = a[i] === b[j] ? lcs[(i + 1) * width + j + 1] + 1 : Math.max(lcs[(i + 1) * width + j], lcs[i * width + j + 1]);
    }
  }
  const out: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      out.push({ kind: "same", text: a[i] });
      i++;
      j++;
    } else if (lcs[(i + 1) * width + j] >= lcs[i * width + j + 1]) {
      out.push({ kind: "del", text: a[i++] });
    } else {
      out.push({ kind: "add", text: b[j++] });
    }
  }
  while (i < a.length) out.push({ kind: "del", text: a[i++] });
  while (j < b.length) out.push({ kind: "add", text: b[j++] });
  return out;
}

// fold 把离改动超过 context 行的连续未变行折成一行"N 行没变"。
function fold(lines: DiffLine[]): DiffLine[] {
  const near = lines.map(() => false);
  lines.forEach((line, i) => {
    if (line.kind === "same") return;
    for (let k = Math.max(0, i - context); k <= Math.min(lines.length - 1, i + context); k++) near[k] = true;
  });
  const out: DiffLine[] = [];
  for (let i = 0; i < lines.length; i++) {
    if (near[i]) {
      out.push(lines[i]);
      continue;
    }
    let j = i;
    while (j < lines.length && !near[j]) j++;
    out.push({ kind: "skip", count: j - i });
    i = j - 1;
  }
  return out;
}
