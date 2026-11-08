// 看差异：一次比较里改了哪些文件(SKILL.md 先看),按行比较一个文件的旧内容和新内容，没变的长段落折起来只留上下文;
// 可以排成一列(行内对比)或左右两列(左右对比);每行标出它在旧内容、新内容里的行号，改掉的一行和替换它的一行再标出改了哪几个词。
import type { VersionDiff } from "@bindings/internal/library";

export type ChangeKind = "added" | "changed" | "deleted";

// changedFiles 是一次比较里改了的文件：SKILL.md 排第一(不论是修改、新增还是删除),其余按修改、新增、删除排。
// 第一项就是打开差异时默认先看的文件。
export function changedFiles(diff: VersionDiff | null | undefined): { kind: ChangeKind; path: string }[] {
  const of = (kind: ChangeKind, paths: string[] | null | undefined) => (paths ?? []).map((path) => ({ kind, path }));
  const all = [...of("changed", diff?.changed), ...of("added", diff?.added), ...of("deleted", diff?.deleted)];
  return all.sort((a, b) => Number(b.path === "SKILL.md") - Number(a.path === "SKILL.md"));
}

export type DiffLine = { kind: "same" | "add" | "del"; text: string } | { kind: "skip"; count: number };

// NumberedLine 是带行号的一行：oldLine 是它在旧内容里的行号，newLine 是在新内容里的(新增的行没有 oldLine,删掉的行没有 newLine);
// 折起来的一段记着它第一行在两边的行号，展开时据此取出这几行。
export type NumberedLine =
  | { kind: "same"; text: string; oldLine: number; newLine: number }
  | { kind: "del"; text: string; oldLine: number }
  | { kind: "add"; text: string; newLine: number }
  | { kind: "skip"; count: number; oldLine: number; newLine: number };

// SplitRow 是左右对比的一行：左边是旧内容(没变或删掉的行),右边是新内容(没变或新增的行),没有对应行的一侧为 undefined。
// 行原样放进来(带行号的照样带着),没变的行两边是同一个对象。
export type SplitRow<L extends DiffLine | NumberedLine = DiffLine> =
  | { kind: "pair"; left?: Extract<L, { kind: "same" | "del" }>; right?: Extract<L, { kind: "same" | "add" }> }
  | Extract<L, { kind: "skip" }>;

// Segment 是一行里的一段文字,changed 为 true 的是这一行和对面那一行不一样的词。
export interface Segment {
  text: string;
  changed: boolean;
}

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

// unfold 把 open 里的几段折起来的行展开成没变的行(文字从旧内容 before 里取),其余照旧。
export function unfold(lines: NumberedLine[], before: string, open: Set<NumberedLine>): NumberedLine[] {
  if (open.size === 0) return lines;
  const old = before.split("\n");
  return lines.flatMap((line) =>
    line.kind === "skip" && open.has(line)
      ? Array.from({ length: line.count }, (_, k) => ({
          kind: "same" as const,
          text: old[line.oldLine - 1 + k] ?? "",
          oldLine: line.oldLine + k,
          newLine: line.newLine + k,
        }))
      : [line],
  );
}

// numberLines 给 lineDiff 的结果标上行号，折起来的行也算进去。
export function numberLines(lines: DiffLine[]): NumberedLine[] {
  let oldLine = 1;
  let newLine = 1;
  return lines.map((line) => {
    switch (line.kind) {
      case "skip": {
        const skip = { kind: "skip" as const, count: line.count, oldLine, newLine };
        oldLine += line.count;
        newLine += line.count;
        return skip;
      }
      case "same":
        return { kind: "same", text: line.text, oldLine: oldLine++, newLine: newLine++ };
      case "del":
        return { kind: "del", text: line.text, oldLine: oldLine++ };
      case "add":
        return { kind: "add", text: line.text, newLine: newLine++ };
    }
  });
}

// splitRows 把 lineDiff 的结果排成左右两列：没变的行两边并排;一段连续的改动里，删掉的行和新增的行按先后一一并排，多出来的一侧对面留空。
export function splitRows<L extends DiffLine | NumberedLine>(lines: L[]): SplitRow<L>[] {
  type Left = Extract<L, { kind: "same" | "del" }>;
  type Right = Extract<L, { kind: "same" | "add" }>;
  const rows: SplitRow<L>[] = [];
  for (const block of blocks(lines)) {
    if (!Array.isArray(block)) {
      rows.push(block.kind === "skip" ? (block as Extract<L, { kind: "skip" }>) : { kind: "pair", left: block as Left, right: block as Right });
      continue;
    }
    const [dels, adds] = block;
    for (let i = 0; i < Math.max(dels.length, adds.length); i++) {
      rows.push({ kind: "pair", left: dels[i] as Left | undefined, right: adds[i] as Right | undefined });
    }
  }
  return rows;
}

// inlineChanges 是每一行改掉的行和替换它的行(一段改动里按先后一一配对，和左右对比并排的是同一对)各自改了哪几个词，按行对象查;
// 两行差得太多(大半都不一样)的不标，整行已经标着改了。
export function inlineChanges<L extends DiffLine | NumberedLine>(lines: L[]): Map<L, Segment[]> {
  const out = new Map<L, Segment[]>();
  for (const block of blocks(lines)) {
    if (!Array.isArray(block)) continue;
    const [dels, adds] = block;
    for (let i = 0; i < Math.min(dels.length, adds.length); i++) {
      const pair = wordDiff(textOf(dels[i]), textOf(adds[i]));
      if (!pair) continue;
      out.set(dels[i], pair[0]);
      out.set(adds[i], pair[1]);
    }
  }
  return out;
}

const textOf = (line: DiffLine | NumberedLine) => (line.kind === "skip" ? "" : line.text);

// blocks 把行分成没变的行、折起来的行和一段连续的改动(删掉的行与新增的行各自按先后)。
function blocks<L extends DiffLine | NumberedLine>(lines: L[]): (L | [L[], L[]])[] {
  const out: (L | [L[], L[]])[] = [];
  let change: [L[], L[]] | null = null;
  for (const line of lines) {
    if (line.kind === "del" || line.kind === "add") {
      if (!change) out.push((change = [[], []]));
      change[line.kind === "del" ? 0 : 1].push(line);
    } else {
      change = null;
      out.push(line);
    }
  }
  return out;
}

// 拉丁字母、数字连成一个词，空白连成一段，其余(标点、汉字)一个字符一段。
const tokenPattern = /[\p{Script=Latin}\p{Script=Greek}\p{Script=Cyrillic}\p{N}_]+|\s+|./gsu;
// 两行的词数相乘超过这个数就不标词，整行照样标着改了。
const maxTokenCells = 1_000_000;
// 两行没变的字(不算空白)占较长一行的比例低于这个数时，算作整行重写，不标词。
const minSimilarity = 0.4;

// wordDiff 按词比较两行，给出两边各自的分段;差得太多或太长时为 undefined。
function wordDiff(before: string, after: string): [Segment[], Segment[]] | undefined {
  const a = before.match(tokenPattern) ?? [];
  const b = after.match(tokenPattern) ?? [];
  if (a.length === 0 || b.length === 0 || a.length * b.length > maxTokenCells) return undefined;
  // 先去掉两头相同的词再比较中间：中间有重复的字(如「全局目录」改成「项目目录」)时，改动连成一块，不碎成几截。
  let head = 0;
  while (head < a.length && head < b.length && a[head] === b[head]) head++;
  let tail = 0;
  while (tail < a.length - head && tail < b.length - head && a[a.length - 1 - tail] === b[b.length - 1 - tail]) tail++;
  const midA = a.slice(head, a.length - tail);
  const midB = b.slice(head, b.length - tail);
  const width = midB.length + 1;
  const lcs = new Uint32Array((midA.length + 1) * width);
  for (let i = midA.length - 1; i >= 0; i--) {
    for (let j = midB.length - 1; j >= 0; j--) {
      lcs[i * width + j] =
        midA[i] === midB[j] ? lcs[(i + 1) * width + j + 1] + 1 : Math.max(lcs[(i + 1) * width + j], lcs[i * width + j + 1]);
    }
  }
  const left: Segment[] = [];
  const right: Segment[] = [];
  let common = 0;
  const same = (text: string) => {
    common += text.trim().length;
    push(left, text, false);
    push(right, text, false);
  };
  a.slice(0, head).forEach(same);
  let i = 0;
  let j = 0;
  while (i < midA.length || j < midB.length) {
    if (i < midA.length && j < midB.length && midA[i] === midB[j]) {
      same(midA[i]);
      i++;
      j++;
    } else if (j >= midB.length || (i < midA.length && lcs[(i + 1) * width + j] >= lcs[i * width + j + 1])) {
      push(left, midA[i++], true);
    } else {
      push(right, midB[j++], true);
    }
  }
  a.slice(a.length - tail).forEach(same);
  const visible = (s: string) => s.replace(/\s+/g, "").length;
  if (common < minSimilarity * Math.max(visible(before), visible(after))) return undefined;
  return [merge(left), merge(right)];
}

function push(segments: Segment[], text: string, changed: boolean) {
  const last = segments.at(-1);
  if (last && last.changed === changed) last.text += text;
  else segments.push({ text, changed });
}

// merge 把夹在两段改动之间、只有空白的没变段并进改动，一句里改了几个相邻的词时标成一整块，不碎成几截;
// 再把改动两头的空白挪回没变的一侧(只改了空白的除外)，标出来的块不拖着空格。
function merge(segments: Segment[]): Segment[] {
  const bridged: Segment[] = [];
  segments.forEach((seg, k) => {
    const bridge = !seg.changed && !seg.text.trim() && segments[k - 1]?.changed && segments[k + 1]?.changed;
    push(bridged, seg.text, seg.changed || !!bridge);
  });
  const out: Segment[] = [];
  for (const seg of bridged) {
    const [, lead, core, trail] = seg.changed && seg.text.trim() ? /^(\s*)(.*?)(\s*)$/s.exec(seg.text)! : [, "", seg.text, ""];
    if (lead) push(out, lead, false);
    push(out, core, seg.changed);
    if (trail) push(out, trail, false);
  }
  return out;
}

// ChangeNode 是改了的文件排成的目录树里的一项：目录(只有一个子目录的连续几层合成一项，名字写成 a/b)或文件。
export type ChangeNode =
  | { kind: "dir"; name: string; path: string; children: ChangeNode[] }
  | { kind: "file"; name: string; path: string; change: ChangeKind };

// changeTree 把改了的文件按路径排成目录树：同一层目录在前、文件在后，各自按名字排(和技能详情的文件树一样)。
export function changeTree(files: { kind: ChangeKind; path: string }[]): ChangeNode[] {
  type Dir = Extract<ChangeNode, { kind: "dir" }>;
  const root: Dir = { kind: "dir", name: "", path: "", children: [] };
  for (const f of files) {
    const parts = f.path.split("/");
    let dir = root;
    parts.slice(0, -1).forEach((name, i) => {
      let next = dir.children.find((c): c is Dir => c.kind === "dir" && c.name === name);
      if (!next) {
        next = { kind: "dir", name, path: parts.slice(0, i + 1).join("/"), children: [] };
        dir.children.push(next);
      }
      dir = next;
    });
    dir.children.push({ kind: "file", name: parts.at(-1)!, path: f.path, change: f.kind });
  }
  const tidy = (nodes: ChangeNode[]): ChangeNode[] =>
    nodes
      .map((n) => {
        if (n.kind === "file") return n;
        let dir = n;
        while (dir.children.length === 1 && dir.children[0].kind === "dir") {
          const only: Dir = dir.children[0];
          dir = { ...only, name: `${dir.name}/${only.name}` };
        }
        return { ...dir, children: tidy(dir.children) };
      })
      .sort((a, b) => Number(a.kind === "file") - Number(b.kind === "file") || a.name.localeCompare(b.name));
  return tidy(root.children);
}

// visibleFiles 是目录树里看得见的文件(折起来的目录 closed 里的不算),按显示的先后;文件列表里用上下键换文件时按这个顺序。
export function visibleFiles(nodes: ChangeNode[], closed: Set<string>): string[] {
  return nodes.flatMap((n) => (n.kind === "file" ? [n.path] : closed.has(n.path) ? [] : visibleFiles(n.children, closed)));
}
