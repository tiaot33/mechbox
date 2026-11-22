// 在 skills.sh 上给未纳管技能找到的来源：来源记录认不出来源的未纳管技能，列出之后在后台去 skills.sh 找来源，找到的陆续补到界面上。
// 认出是库里哪个技能的(和它一样，或是它在来源里的新版)跟着那个技能的来源，不找。
// 主界面的快照每读到一次(读快照本身只读数据),就给其中还没找过的发起一轮;快照没变时也照样看一遍，上次出错没找成的这时再找。
import { useSyncExternalStore } from "react";
import { hashKey } from "@tanstack/react-query";
import { LibraryService } from "@bindings/cmd/desktop";
import type { PlannedUnmanagedSkill, Scope, UnmanagedSkill } from "@bindings/internal/library";
import { queryClient, snapshotQuery } from "./library";
import { scopeKey } from "./scope";

// found 按作用域、技能目录与文件夹名记下这次打开 app 以来找过的：值是找到的来源，找过没找到的为 ""，不再重复找;
// 找的过程中出错(取消、技能库报错)的不记，下次读到快照时再找。
let found: ReadonlyMap<string, string> = new Map();
const searching = new Set<string>();
// round 在拉回整库时加一：之前发出、晚返回的那一轮按旧库的作用域找的，结果不再记下。
let round = 0;
const listeners = new Set<() => void>();

// 同一个作用域的几个技能目录里可以有同名的文件夹，按目录分开记。
function folderKey(scope: Scope, directory: string | undefined, name: string): string {
  return `${scopeKey(scope)}/${directory ?? ""}/${name}`;
}

function notify() {
  for (const listener of listeners) listener();
}

function findSources(unmanaged: UnmanagedSkill[]) {
  const pending = unmanaged.filter((u) => {
    const key = folderKey(u.scope, u.directory, u.name);
    return !u.source_input && !u.skill_id && !u.source_latest_of && !found.has(key) && !searching.has(key);
  });
  if (pending.length === 0) return;
  const keys = pending.map((u) => folderKey(u.scope, u.directory, u.name));
  for (const key of keys) searching.add(key);
  const started = round;
  LibraryService.FindUnmanagedSources({ folders: pending.map((u) => ({ scope: u.scope, directory: u.directory, name: u.name })) }).then(
    (results) => {
      if (started !== round) return;
      const next = new Map(found);
      for (const key of keys) next.set(key, "");
      for (const r of results ?? []) next.set(folderKey(r.scope, r.directory, r.name), r.source_input);
      for (const key of keys) searching.delete(key);
      found = next;
      notify();
    },
    () => {
      if (started === round) for (const key of keys) searching.delete(key);
    },
  );
}

// 快照每读到一次就看一遍;写操作流程用 setQueryData 改快照里的设置(manual)不算读到。
const snapshotHash = hashKey(snapshotQuery.queryKey);
queryClient.getQueryCache().subscribe((event) => {
  if (event.type !== "updated" || event.action.type !== "success" || event.action.manual) return;
  if (event.query.queryHash !== snapshotHash) return;
  const s = queryClient.getQueryData(snapshotQuery.queryKey);
  if (s) findSources(s.unmanaged);
});

// forgetFoundSources 在拉回整库之后清掉找到的来源(按作用域记着，作用域里带项目 ID),还没返回的那一轮作废。
// 由 view.ts 的 forgetLibrary 调用。
export function forgetFoundSources() {
  round++;
  found = new Map();
  searching.clear();
  notify();
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => void listeners.delete(listener);
}

// FoundSources 是在 skills.sh 上找到的来源，用 foundSource 取一个未纳管技能的。
export type FoundSources = ReadonlyMap<string, string>;

// useFoundSources 订阅在 skills.sh 上找到的来源：找到新的时重新渲染。
export function useFoundSources(): FoundSources {
  return useSyncExternalStore(subscribe, () => found);
}

// foundSource 是在 skills.sh 上给这个未纳管技能找到的来源;来源记录认得出、还没找完或没找到时为 undefined。
// 认出是库里哪个技能的(和它一样，或是它在来源里的新版)跟着那个技能的来源，之前找到的来源不再用。
export function foundSource(sources: FoundSources, skill: UnmanagedSkill): string | undefined {
  if (skill.source_input || skill.skill_id || skill.source_latest_of) return undefined;
  return sources.get(folderKey(skill.scope, skill.directory, skill.name)) || undefined;
}

// withFoundSource 给来源记录认不出来源的那一条补上在 skills.sh 上找到的来源(取第一处找到的),
// 和来源记录认出的一样预填、分组。
export function withFoundSource(sources: FoundSources, planned: PlannedUnmanagedSkill): PlannedUnmanagedSkill {
  if (planned.source_input || planned.error || planned.skill_id || planned.source_latest_of) return planned;
  for (const folder of planned.folders ?? []) {
    const input = foundSource(sources, folder);
    if (input) return { ...planned, source_input: input, source_ref: undefined };
  }
  return planned;
}
