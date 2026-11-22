// 自定义 Agent(agents.tsx)里的判断：改目录时哪些已装的技能、组安装受影响要先问，删 Agent 时哪些文件夹归它、哪些还有别的 Agent 在读。
// 不碰界面、不调技能库，单元测试见 agents-rules.test.ts。
import type { Agent, AgentCode, AgentInput, Scope } from "@bindings/internal/library";
import { detachedBy } from "@/lib/consent";
import type { Snapshot } from "@/lib/library";
import { folderPlaces } from "@/lib/installs";
import { placeOf } from "@/lib/scope";

// cleanPath 是比较目录时去掉开头的 ./ 和末尾的 /:写法不同的同一个目录不算改了。
const cleanPath = (path: string) => path.replace(/^\.\//, "").replace(/\/+$/, "");

// agentEdit 是改自定义 Agent 时受影响的：某一层级(全局或项目)的目录改了，这个 Agent 在那一层已装的技能(affected,
// 含因共用目录而算已装的)和组安装数(groupInstalls)。asks 是要不要先问是否迁移;shared 是其中还有别的 Agent 在读的
// (那份文件夹留在旧目录);detached 是迁移时别处的符号链接指着要搬走的文件夹、库先给那几处换上独立的一份的。
export function agentEdit(s: Snapshot, agent: Agent, input: AgentInput) {
  const dropped = (now: string[] | null, before: string[] | null) => !(now ?? []).map(cleanPath).includes(cleanPath(before?.[0] ?? ""));
  const globalChanged = dropped(input.global_directories, agent.global_directories);
  const projectChanged = dropped(input.project_directories, agent.project_directories);
  const changedHere = (scope: Scope) => scope.agent === agent.code && (placeOf(scope) ? projectChanged : globalChanged);
  const affected = s.skillScopes.filter((p) => changedHere(p.scope));
  const groupInstalls = s.groupInstalls.filter((g) => changedHere(g.scope)).length;
  return {
    affected,
    groupInstalls,
    asks: affected.length > 0 || groupInstalls > 0,
    shared: affected.filter((p) => p.shared_with?.length),
    detached: detachedBy(folderPlaces(s, affected.filter((p) => !p.shared_with?.length))),
  };
}

// agentDelete 是删除自定义 Agent 时它装着的各处:own 是只有它在读的(选删掉时删),kept 是还有别的 Agent 在读的，不管怎么选都保留。
export function agentDelete(s: Snapshot, code: AgentCode) {
  const places = s.skillScopes.filter((p) => p.scope.agent === code);
  return { kept: places.filter((p) => p.shared_with?.length), own: places.filter((p) => !p.shared_with?.length) };
}
