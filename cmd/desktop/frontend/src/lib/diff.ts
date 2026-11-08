// 看差异：一次比较里改了哪些文件(SKILL.md 先看、排成目录树),按行比较一个文件的旧内容和新内容，没变的长段落折起来只留上下文;
// 可以排成一列(行内对比)或左右两列(左右对比);每行标出它在旧内容、新内容里的行号，改掉的一行和替换它的一行再标出改了哪几个词。
import type { VersionDiff } from "@bindings/internal/library";
import { fileTree, type TreeNode } from "./file-tree";

export type ChangeKind = "added" | "changed" | "deleted";

// changedFiles 是一次比较里改了的文件：SKILL.md 排第一(不论是修改、新增还是删除),其余按修改、新增、删除排。
// 第一项就是打开差异时默认先看的文件。
export function changedFiles(diff: VersionDiff | null | undefined): { kind: ChangeKind; path: string }[] {
  const of = (kind: ChangeKind, paths: string[] | null | undefined) => (paths ?? []).map((path) => ({ kind, path }));
  const all = [...of("changed", diff?.changed), ...of("added", diff?.added), ...of("deleted", diff?.deleted)];
  return all.sort((a, b) => Number(b.path === "SKILL.md") - Number(a.path === "SKILL.md"));
}

// DiffLine 是差异里的一行：oldLine 是它在旧内容里的行号，newLine 是在新内容里的(新增的行没有 oldLine,删掉的行没有 newLine);
// skip 是折起来的一段没变的行，记着它第一行在两边的行号，展开时据此取出这几行。
export type DiffLine =
  | { kind: "same"; text: string; oldLine: number; newLine: number }
  | { kind: "del"; text: string; oldLine: number }
  | { kind: "add"; text: string; newLine: number }
  | { kind: "skip"; count: number; oldLine: number; newLine: number };

type Del = Extract<DiffLine, { kind: "del" }>;
type Add = Extract<DiffLine, { kind: "add" }>;

// SplitRow 是左右对比的一行：左边是旧内容(没变或删掉的行),右边是新内容(没变或新增的行),没有对应行的一侧为 undefined;
// 没变的行两边是同一个对象。折起来的一段原样横跨两边。
export type SplitRow =
  | {
      kind: "pair";
      left?: Extract<DiffLine, { kind: "same" | "del" }>;
      right?: Extract<DiffLine, { kind: "same" | "add" }>;
    }
  | Extract<DiffLine, { kind: "skip" }>;

// Segment 是一行里的一段文字,changed 为 true 的是这一行和对面那一行不一样的词。
export interface Segment {
  text: string;
  changed: boolean;
}

type Op = { kind: "same" | "del" | "add"; text: string };

// compare 按最长公共子序列比较两串(行或词):先去掉两头相同的，再比较中间。
// 中间两边的个数相乘超过 max 时不逐个比较，整段当作删掉再加上，exact 为 false。
function compare(a: string[], b: string[], max: number): { ops: Op[]; exact: boolean } {
  let head = 0;
  while (head < a.length && head < b.length && a[head] === b[head]) head++;
  let tail = 0;
  while (tail < a.length - head && tail < b.length - head && a[a.length - 1 - tail] === b[b.length - 1 - tail]) tail++;
  const midA = a.slice(head, a.length - tail);
  const midB = b.slice(head, b.length - tail);
  const same = (text: string): Op => ({ kind: "same", text });
  const ops: Op[] = a.slice(0, head).map(same);
  const exact = midA.length * midB.length <= max;
  if (!exact) {
    ops.push(...midA.map((text): Op => ({ kind: "del", text })), ...midB.map((text): Op => ({ kind: "add", text })));
  } else {
    // lcs[i][j] 是 midA[i:] 与 midB[j:] 的最长公共子序列长度。
    const width = midB.length + 1;
    const lcs = new Uint32Array((midA.length + 1) * width);
    for (let i = midA.length - 1; i >= 0; i--) {
      for (let j = midB.length - 1; j >= 0; j--) {
        lcs[i * width + j] =
          midA[i] === midB[j] ? lcs[(i + 1) * width + j + 1] + 1 : Math.max(lcs[(i + 1) * width + j], lcs[i * width + j + 1]);
      }
    }
    let i = 0;
    let j = 0;
    while (i < midA.length || j < midB.length) {
      if (i < midA.length && j < midB.length && midA[i] === midB[j]) {
        ops.push(same(midA[i]));
        i++;
        j++;
      } else if (j >= midB.length || (i < midA.length && lcs[(i + 1) * width + j] >= lcs[i * width + j + 1])) {
        ops.push({ kind: "del", text: midA[i++] });
      } else {
        ops.push({ kind: "add", text: midB[j++] });
      }
    }
  }
  ops.push(...a.slice(a.length - tail).map(same));
  return { ops, exact };
}

// 两边行数相乘超过这个数就不逐行比较，整段当作删掉再加上，免得大文件卡住界面。
const maxLineCells = 4_000_000;
// 改动上下各留几行没变的行。
const context = 3;
// 连续没变的行少于这个数时不折：折起来的灰带不比这几行矮多少，还要多点一下。
const minFold = 3;

// lineDiff 按行比较旧内容 before 和新内容 after,标上行号，离改动远的没变行折起来。
export function lineDiff(before: string, after: string): DiffLine[] {
  // 两边(没有的一侧不算)都以换行结尾时，末尾的换行只是行的结束，不显示成多出来的空行;
  // 只有一边有时照常拆开，末尾多出的空行就是这处改动。
  const trim = (!before || before.endsWith("\n")) && (!after || after.endsWith("\n"));
  const split = (text: string) => (text ? (trim ? text.slice(0, -1) : text).split("\n") : []);
  let oldLine = 1;
  let newLine = 1;
  const lines = compare(split(before), split(after), maxLineCells).ops.map((op): DiffLine => {
    if (op.kind === "del") return { kind: "del", text: op.text, oldLine: oldLine++ };
    if (op.kind === "add") return { kind: "add", text: op.text, newLine: newLine++ };
    return {
      kind: "same",
      text: op.text,
      oldLine: oldLine++,
      newLine: newLine++,
    };
  });
  return fold(lines);
}

// fold 把离改动超过 context 行的连续没变行折成一行"N 行没变",不到 minFold 行的照常显示。
function fold(lines: DiffLine[]): DiffLine[] {
  const near = lines.map(() => false);
  lines.forEach((line, i) => {
    if (line.kind === "same") return;
    for (let k = Math.max(0, i - context); k <= Math.min(lines.length - 1, i + context); k++) near[k] = true;
  });
  const out: DiffLine[] = [];
  for (let i = 0; i < lines.length; i++) {
    let j = i;
    while (j < lines.length && !near[j]) j++;
    if (j - i < minFold) {
      out.push(lines[i]);
      continue;
    }
    const first = lines[i] as Extract<DiffLine, { kind: "same" }>;
    out.push({
      kind: "skip",
      count: j - i,
      oldLine: first.oldLine,
      newLine: first.newLine,
    });
    i = j - 1;
  }
  return out;
}

// unfold 把 open 里的几段折起来的行展开成没变的行(文字从旧内容 before 里取),其余照旧。
export function unfold(lines: DiffLine[], before: string, open: Set<DiffLine>): DiffLine[] {
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

// splitRows 把差异排成左右两列：没变的行两边并排;一段连续的改动里，删掉的行和新增的行按先后一一并排，多出来的一侧对面留空。
export function splitRows(lines: DiffLine[]): SplitRow[] {
  const rows: SplitRow[] = [];
  for (const block of blocks(lines)) {
    if (!Array.isArray(block)) {
      rows.push(block.kind === "skip" ? block : { kind: "pair", left: block, right: block });
      continue;
    }
    const [dels, adds] = block;
    for (let i = 0; i < Math.max(dels.length, adds.length); i++) rows.push({ kind: "pair", left: dels[i], right: adds[i] });
  }
  return rows;
}

// changedWords 是改掉的行和替换它的行(一段改动里按先后一一配对，和左右对比并排的是同一对)各自改了哪几个词，按行对象查;
// 两行差得太多(大半都不一样)的不标，整行已经标着改了。
export function changedWords(lines: DiffLine[]): Map<DiffLine, Segment[]> {
  const out = new Map<DiffLine, Segment[]>();
  for (const block of blocks(lines)) {
    if (!Array.isArray(block)) continue;
    const [dels, adds] = block;
    for (let i = 0; i < Math.min(dels.length, adds.length); i++) {
      const pair = wordDiff(dels[i].text, adds[i].text);
      if (!pair) continue;
      out.set(dels[i], pair[0]);
      out.set(adds[i], pair[1]);
    }
  }
  return out;
}

// blocks 把行分成没变的行、折起来的行和一段连续的改动(删掉的行与新增的行各自按先后)。
function blocks(lines: DiffLine[]): (Extract<DiffLine, { kind: "same" | "skip" }> | [Del[], Add[]])[] {
  const out: (Extract<DiffLine, { kind: "same" | "skip" }> | [Del[], Add[]])[] = [];
  let change: [Del[], Add[]] | null = null;
  for (const line of lines) {
    if (line.kind === "same" || line.kind === "skip") {
      change = null;
      out.push(line);
      continue;
    }
    if (!change) out.push((change = [[], []]));
    if (line.kind === "del") change[0].push(line);
    else change[1].push(line);
  }
  return out;
}

// 拉丁字母、数字连成一个词，空白连成一段，其余(标点、汉字)一个字符一段。
const tokenPattern = /[\p{Script=Latin}\p{Script=Greek}\p{Script=Cyrillic}\p{N}_]+|\s+|./gsu;
// 两行中间不同的那段词数相乘超过这个数就不标词，整行照样标着改了。
const maxTokenCells = 1_000_000;
// 两行没变的字(不算空白)占较长一行的比例低于这个数时，算作整行重写，不标词。
const minSimilarity = 0.4;

// wordDiff 按词比较两行，给出两边各自的分段;差得太多或太长时为 undefined。
// 先去掉两头相同的词再比较中间(compare 就是这么做的):中间有重复的字(如「全局目录」改成「项目目录」)时，改动连成一块，不碎成几截。
function wordDiff(before: string, after: string): [Segment[], Segment[]] | undefined {
  const a = before.match(tokenPattern) ?? [];
  const b = after.match(tokenPattern) ?? [];
  if (a.length === 0 || b.length === 0) return undefined;
  const { ops, exact } = compare(a, b, maxTokenCells);
  if (!exact) return undefined;
  const left: Segment[] = [];
  const right: Segment[] = [];
  let common = 0;
  for (const op of ops) {
    if (op.kind === "same") {
      common += op.text.trim().length;
      push(left, op.text, false);
      push(right, op.text, false);
    } else {
      push(op.kind === "del" ? left : right, op.text, true);
    }
  }
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
    if (!seg.changed || !seg.text.trim()) {
      push(out, seg.text, seg.changed);
      continue;
    }
    const start = seg.text.length - seg.text.trimStart().length;
    const end = seg.text.trimEnd().length;
    if (start > 0) push(out, seg.text.slice(0, start), false);
    push(out, seg.text.slice(start, end), true);
    if (end < seg.text.length) push(out, seg.text.slice(end), false);
  }
  return out;
}

// ChangeNode 是改了的文件排成的目录树里的一项：目录或文件。
// 排法和技能详情的文件树一样(fileTree);另外只有一个子目录的连续几层合成一项，名字写成 a/b,技能详情不合。
export type ChangeNode =
  { kind: "dir"; name: string; path: string; children: ChangeNode[] } | { kind: "file"; name: string; path: string; change: ChangeKind };

// changeTree 把改了的文件排成目录树。
export function changeTree(files: { kind: ChangeKind; path: string }[]): ChangeNode[] {
  const convert = (n: TreeNode<{ kind: ChangeKind; path: string }>): ChangeNode => {
    if (n.item) return { kind: "file", name: n.name, path: n.path, change: n.item.kind };
    let dir = n;
    while (dir.children.length === 1 && !dir.children[0].item) {
      const only = dir.children[0];
      dir = { ...only, name: `${dir.name}/${only.name}` };
    }
    return {
      kind: "dir",
      name: dir.name,
      path: dir.path,
      children: dir.children.map(convert),
    };
  };
  return fileTree(files).map(convert);
}

// treeFiles 是目录树里的文件按显示的先后;closed 给出时，折起来的目录里的不算。
export function treeFiles(nodes: ChangeNode[], closed?: Set<string>): string[] {
  return nodes.flatMap((n) => (n.kind === "file" ? [n.path] : closed?.has(n.path) ? [] : treeFiles(n.children, closed)));
}

// stepFile 是文件列表里按上下键(step 为 1 或 -1)从 path 换到的文件：按显示的先后找下一个看得见的;
// path 自己在折起来的目录里时，从它在整棵树里的位置往前或往后找。到头了为 undefined。
export function stepFile(nodes: ChangeNode[], closed: Set<string>, path: string, step: 1 | -1): string | undefined {
  const all = treeFiles(nodes);
  const visible = new Set(treeFiles(nodes, closed));
  for (let i = all.indexOf(path) + step; i >= 0 && i < all.length; i += step) {
    if (visible.has(all[i])) return all[i];
  }
  return undefined;
}
