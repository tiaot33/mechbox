// Agent(agents.tsx、agent-settings.tsx)里的判断：改目录时哪些已装的技能、组安装受影响要先问，移除 Agent 时哪些文件夹归它、
// 哪些还有别的 Agent 在读，候选池怎么排、哪几个要标「已有 Agent 读这个目录」。
// 不碰界面、不调技能库，单元测试见 agents-rules.test.ts。
import type { Agent, AgentCode, AgentInput, Scope, SkillScopeInspection } from "@bindings/internal/library";
import { detachedBy } from "@/lib/consent";
import type { Snapshot } from "@/lib/library";
import { folderPlaces } from "@/lib/installs";
import { directoriesAt } from "@/lib/places";
import { placeOf, type Place } from "@/lib/scope";

// cleanPath 是比较目录时去掉开头的 ./ 和末尾的 /:写法不同的同一个目录不算改了。
const cleanPath = (path: string) => path.replace(/^\.\//, "").replace(/\/+$/, "");

const inputDirectories = (input: AgentInput, place: Place) => ((place ? input.project_directories : input.global_directories) ?? []).map(cleanPath);

// agentEdit 是编辑 Agent 时受影响的:affected 是这个 Agent 所在目录被去掉的那几处安装(含因共用目录而算已装的;
// 所在目录还在新列表里的磁盘不动，不算);groupInstalls 是原来的第 1 个目录被去掉的层级里它的组安装数。
// asks 是要不要先问是否迁移;shared 是其中还有别的 Agent 在读的(那份文件夹留在旧目录);detached 是迁移时别处的符号链接
// 指着要搬走的文件夹、库先给那几处换上独立的一份的。
export function agentEdit(s: Snapshot, agent: Agent, input: AgentInput) {
  const removed = (dir: string, place: Place) => !inputDirectories(input, place).includes(cleanPath(dir));
  // 这一处所在的目录：技能库总会给出;没有时按原来的第 1 个目录算。
  const dirOf = (p: SkillScopeInspection) => p.directory || (directoriesAt(agent, placeOf(p.scope))[0] ?? "");
  const firstRemoved = (scope: Scope) => removed(directoriesAt(agent, placeOf(scope))[0] ?? "", placeOf(scope));
  const affected = s.skillScopes.filter((p) => p.scope.agent === agent.code && removed(dirOf(p), placeOf(p.scope)));
  const groupInstalls = s.groupInstalls.filter((g) => g.scope.agent === agent.code && firstRemoved(g.scope)).length;
  return {
    affected,
    groupInstalls,
    asks: affected.length > 0 || groupInstalls > 0,
    shared: affected.filter((p) => p.shared_with?.length),
    detached: detachedBy(folderPlaces(s, affected.filter((p) => !p.shared_with?.length))),
  };
}

// agentDelete 是移除 Agent 时它装着的各处:own 是只有它在读的(选删掉时删),kept 是还有别的 Agent 在读的(含只因共用目录才算已装的),
// 不管怎么选都保留。
export function agentDelete(s: Snapshot, code: AgentCode) {
  const places = s.skillScopes.filter((p) => p.scope.agent === code);
  return { kept: places.filter((p) => p.shared_with?.length), own: places.filter((p) => !p.shared_with?.length) };
}

// ---------- 候选池 ----------

// poolMatches 是候选池里按名称、目录搜索:不分大小写，名称或任一目录包含搜索的文字。
export function poolMatches(agent: Agent, search: string): boolean {
  const q = search.trim().toLowerCase();
  if (!q) return true;
  return [agent.name, ...directoriesAt(agent, 0), ...directoriesAt(agent, 1)].some((x) => x.toLowerCase().includes(q));
}

// sortPool 是候选池的顺序：检测到的排在前面，其余按名称。
export function sortPool(pool: Agent[]): Agent[] {
  const collator = new Intl.Collator(undefined, { sensitivity: "base", numeric: true });
  return [...pool].sort((a, b) => Number(b.detected) - Number(a.detected) || collator.compare(a.name, b.name));
}

// AlreadyRead 是候选池里一个 Agent 独有的目录已经有已添加的 Agent 在读:dir 是那个目录(全局为绝对路径，项目为相对路径),by 是在读的那几个。
export type AlreadyRead = { dir: string; project: boolean; by: Agent[] };

// alreadyRead 是候选池里这个 Agent 独有的目录(候选池里只有它读的，如 Cursor 的 .cursor/skills;不比 .agents/skills 这种许多 Agent 都读的)
// 已经有已添加的 Agent 在读的那几个:多半是以前手填添加的同一个 Agent,再添加就有两个 Agent 读同一个目录。只标出，不阻止添加。
export function alreadyRead(pool: Agent[], added: Agent[], agent: Agent): AlreadyRead[] {
  return [0, 1].flatMap((place) =>
    directoriesAt(agent, place).flatMap((dir): AlreadyRead[] => {
      const same = (other: Agent) => directoriesAt(other, place).some((d) => cleanPath(d) === cleanPath(dir));
      if (pool.some((other) => other.code !== agent.code && same(other))) return [];
      const by = added.filter(same);
      return by.length ? [{ dir, project: !!place, by }] : [];
    }),
  );
}

// detectedPicks 是升级上来以后提示添加 Agent 时默认勾上的：候选池里在这台机器上检测到的;独有的目录已经有已添加的 Agent 在读的
// (多半是以前手填添加的同一个 Agent,见 alreadyRead)不勾，免得两个 Agent 读同一个目录。
export function detectedPicks(pool: Agent[], added: Agent[]): string[] {
  return pool.filter((a) => a.detected && !alreadyRead(pool, added, a).length).map((a) => a.code);
}

// undetected 是设置页一行要不要标「未检测到」:有检测目录、又都不存在时才标;检测目录为空的不检测，不标。
export function undetected(agent: Agent): boolean {
  return !agent.detected && (agent.detect_directories ?? []).length > 0;
}
