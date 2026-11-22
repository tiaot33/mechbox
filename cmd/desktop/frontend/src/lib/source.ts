// 来源与集合：来源是哪一种、在界面上叫什么，集合里有哪些技能、在哪个来源版本上。
// 来源那边有没有新内容见 lib/source-state。
import { SourceType, type Collection, type CollectionID, type Scope, type Skill, type Source } from "@bindings/internal/library";
import { messages } from "./i18n";
import type { Snapshot } from "./library";
import { lastSegment, shortPath } from "./paths";

// ---------- 来源是哪一种 ----------

// isLocal 是本地目录来源：作者就是用户自己，不检查更新、不进更新页，技能上只有「重新读取」。
export function isLocal(source: Source): boolean {
  return source.type === SourceType.SourceLocal;
}

// noSource 是没有来源的技能：从作用域入库的未纳管技能，作者就是用户自己。不在任何集合里，不检查更新，
// 只能用目录里的修改更新(在装出去的地方改了以后，那一处会出现"用这里的修改更新")。
export function noSource(source: Source): boolean {
  return source.type === SourceType.$zero;
}

// userAuthored 是作者就是用户自己的来源：本地目录来源、没有来源。它们不跟着别人的来源版本走，不检查更新、不计入角标。
export function userAuthored(source: Source): boolean {
  return isLocal(source) || noSource(source);
}

// ---------- 来源叫什么 ----------

export function sourceKindLabel(type: SourceType): string {
  return {
    [SourceType.SourceGitHub]: "GitHub",
    [SourceType.SourceGit]: "Git",
    [SourceType.SourceURL]: "URL",
    [SourceType.SourceWellKnown]: "well-known",
    [SourceType.SourceLocal]: messages().model.kindLocal,
    [SourceType.$zero]: messages().model.noSource,
  }[type];
}

// sourceLabel 是集合在左栏里的名字：GitHub 取 owner/repo,Git 仓库取主机加仓库路径,well-known 取站点主机加路径,
// URL 取文件名，本地取缩写路径。
export function sourceLabel(s: Snapshot, source: Source): string {
  switch (source.type) {
    case SourceType.SourceGitHub:
    case SourceType.SourceGit:
    case SourceType.SourceWellKnown:
      return source.label;
    case SourceType.SourceURL:
      return lastSegment(source.label);
    case SourceType.SourceLocal:
      return shortPath(s, source.label);
    case SourceType.$zero:
      return messages().model.noSource;
    default:
      return messages().model.unknownSource;
  }
}

// sourceShort 是技能行里的来源短名：GitHub 取 owner,Git 仓库取主机后的第一段(owner 或 group),well-known 取站点主机,
// URL 取文件名;本地目录写目录(和左栏、节标题的名字一样，同一个来源在各处叫同一个名字),没有来源的写"没有来源"。
export function sourceShort(s: Snapshot, source: Source): string {
  switch (source.type) {
    case SourceType.SourceGitHub:
      return sourceLabel(s, source).split("/")[0];
    case SourceType.SourceGit: {
      const parts = source.label.split("/");
      return parts[1] ?? parts[0];
    }
    case SourceType.SourceWellKnown:
      return source.label.split("/")[0];
    case SourceType.SourceURL:
      return sourceLabel(s, source).replace(/\.zip$/, "");
    case SourceType.SourceLocal:
      return sourceLabel(s, source);
    default:
      return sourceKindLabel(source.type);
  }
}

// ---------- 集合 ----------

export function collectionSkills(s: Snapshot, c: Collection): Skill[] {
  const ids = new Set(c.skills ?? []);
  return s.skills.filter((x) => ids.has(x.id));
}

// foldCollections 把左栏的集合分成照常列出的与折起来的：开着折叠时，技能少于 foldBelow 个的集合折起来，两边都保持原来的先后;
// 关着时、或没有一个集合够数时(折起来就只剩一行「另外 K 个」)全部照常列出。
export function foldCollections(s: Pick<Snapshot, "collections" | "foldCollections" | "foldBelow">): { shown: Collection[]; folded: Collection[] } {
  const small = (c: Collection) => (c.skills?.length ?? 0) < s.foldBelow;
  const shown = s.collections.filter((c) => !small(c));
  if (!s.foldCollections || shown.length === 0) return { shown: s.collections, folded: [] };
  return { shown, folded: s.collections.filter(small) };
}

// collectionName 是更新页的集合行、更新计划和更新提示里集合的名字：只有一个技能时用技能名，否则是来源名。
export function collectionName(s: Snapshot, collectionID: CollectionID): string {
  const c = s.collections.find((x) => x.id === collectionID);
  if (!c) return messages().update.collectionFallback(collectionID);
  const skills = collectionSkills(s, c);
  return skills.length === 1 ? skills[0].name : sourceLabel(s, c.source);
}

// ---------- 来源版本 ----------

export function shortRevision(revision: string): string {
  return revision.slice(0, 7);
}

// revisionSummary 说明集合里的技能在哪个来源版本上：都在同一个时是 same("在 a1b2c3");
// 不在同一个时是 mixed,直接写"pdf 在 d4e5f6,其余 4 个在 a1b2c3",不起专门的名字。
// 来源版本未知的(入库时和来源里的不一样)不算进任何一个，单独写"grill-me 的来源版本未知";都未知时是 unknown("来源版本未知")。
// 本地目录来源与没有来源的技能为 undefined。文案见 revisionText。
export type RevisionSummary =
  | { kind: "unknown" }
  | { kind: "same"; revision: string }
  | { kind: "mixed"; others: { names: string[]; revision: string }[]; unknown: string[]; rest: number; revision: string };

export function revisionSummary(skills: Skill[]): RevisionSummary | undefined {
  const tracked = skills.filter((x) => !userAuthored(x.source));
  const unknown = tracked.filter((x) => !x.revision);
  const byRevision = new Map<string, Skill[]>();
  for (const x of tracked) {
    if (!x.revision) continue;
    const key = shortRevision(x.revision);
    byRevision.set(key, [...(byRevision.get(key) ?? []), x]);
  }
  const names = (list: Skill[]) => list.map((x) => x.name);
  if (byRevision.size === 0) return unknown.length ? { kind: "unknown" } : undefined;
  const groups = [...byRevision.entries()].sort((a, b) => b[1].length - a[1].length);
  const [main, ...others] = groups;
  if (!others.length && !unknown.length) return { kind: "same", revision: main[0] };
  return {
    kind: "mixed",
    others: others.map(([revision, list]) => ({ names: names(list), revision })),
    unknown: names(unknown),
    rest: main[1].length,
    revision: main[0],
  };
}

export function revisionText(summary: RevisionSummary | undefined): string {
  const m = messages().model;
  switch (summary?.kind) {
    case "unknown":
      return m.revisionUnknown;
    case "same":
      return m.revisionSame(summary.revision);
    case "mixed":
      return m.revisionMixed(summary);
    default:
      return "";
  }
}

// ---------- 来源记录记着的来源 ----------

// sourceWithRef 是界面上写一个来源链接连同分支的样子:「owner/repo · main」，没有分支时只写链接。
export function sourceWithRef(input: string, ref?: string): string {
  return ref ? `${input} · ${ref}` : input;
}

// RecordedSource 是没有来源的技能在 npx skills 的来源记录里记着的一个来源:places 是记着它的那几处(装着的，或显示在技能下面的未纳管技能)。
export interface RecordedSource {
  input: string;
  ref?: string;
  places: Scope[];
}

// recordedSources 列出没有来源的技能在各处的来源记录里记着的来源，同一个来源(连同分支)合成一条，按第一次出现的先后;
// 技能有来源时为空。技能详情据此提示可以把来源接上(AttachSource)。
export function recordedSources(s: Pick<Snapshot, "skillScopes" | "unmanaged">, skill: Skill): RecordedSource[] {
  if (!noSource(skill.source)) return [];
  const found: RecordedSource[] = [];
  for (const place of [...s.skillScopes, ...s.unmanaged]) {
    if (place.skill_id !== skill.id || !place.source_input) continue;
    const ref = place.source_ref || undefined;
    const same = found.find((x) => x.input === place.source_input && x.ref === ref);
    if (same) same.places.push(place.scope);
    else found.push({ input: place.source_input, ref, places: [place.scope] });
  }
  return found;
}
