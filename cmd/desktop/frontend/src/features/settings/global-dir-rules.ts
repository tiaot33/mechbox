// 设置页换全局目录(settings-page.tsx)里的判断：换之前要不要先确认，确认框里怎样称呼换前、换后的位置。
// 不碰界面、不调技能库，单元测试见 global-dir-rules.test.ts。
import { messages } from "@/lib/i18n";
import type { Snapshot } from "@/lib/library";
import { placeCount, type AgentList } from "@/lib/places";
import { placeOf } from "@/lib/scope";

// globalInstallsToConfirm 是把全局目录从 before 换到 after(都是 WSL 发行版的名字，空字符串为 Windows 本机)之前要先确认的全局安装有几处:
// 库里记着全局安装时换过去，这些安装都改按新位置的目录检查，先说明再换;选回同一个位置、库里没有全局安装时为 0,直接换。
// 只因和别的 Agent 读同一个目录才算已装的(shared_only)不是库里记着的安装，不算;合成一处的几个 Agent 只算一处。
export function globalInstallsToConfirm(s: AgentList & Pick<Snapshot, "skillScopes">, before: string, after: string): number {
  if (before === after) return 0;
  return placeCount(s, s.skillScopes.filter((p) => !placeOf(p.scope) && !p.shared_only));
}

// globalDirName 是全局目录所在位置的叫法，和设置里「全局目录」的下拉选项一样：Windows 本机，或「WSL · 发行版」。
export function globalDirName(distro: string): string {
  const t = messages();
  return distro ? t.paths.wsl(distro) : t.settings.windowsHome;
}
