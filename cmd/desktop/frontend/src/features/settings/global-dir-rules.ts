// 设置页换全局目录(settings-page.tsx)里的判断：现在实际用的是哪里，换之前要不要先确认，换过去以后要不要补弹添加 Agent,
// 以及确认框、下拉选项里怎样称呼一个位置。位置都用 WSL 发行版的名字表示，空字符串为 Windows 本机。
// 不碰界面、不调技能库，单元测试见 global-dir-rules.test.ts。
import { messages } from "@/lib/i18n";
import type { Snapshot } from "@/lib/library";
import { placeCount, type AgentList } from "@/lib/places";
import { placeOf } from "@/lib/scope";

// globalDirInUse 是全局目录现在实际在哪：设置里选的发行版启动时没能打开(distroError 不为空)时，技能库用的是 Windows 本机的全局目录，
// 虽然设置里仍记着那个发行版;否则就是设置里选的。
export function globalDirInUse(chosen: string, distroError: string): string {
  return distroError ? "" : chosen;
}

// globalInstallsToConfirm 是把全局目录从 before(现在实际用的，见 globalDirInUse)换到 after 之前要先确认的全局安装有几处:
// 库里记着全局安装时换过去，这些安装都改按新位置的目录检查，先说明再换;实际用的目录不变(如发行版打不开、已经在用 Windows 本机时选回 Windows 本机)、
// 库里没有全局安装时为 0,直接换。只因和别的 Agent 读同一个目录才算已装的(shared_only)不是库里记着的安装，不算;合成一处的几个 Agent 只算一处。
export function globalInstallsToConfirm(s: AgentList & Pick<Snapshot, "skillScopes">, before: string, after: string): number {
  if (before === after) return 0;
  return placeCount(s, s.skillScopes.filter((p) => !placeOf(p.scope) && !p.shared_only));
}

// switchedToDistro 是这次保存有没有把全局目录换到另一个 WSL 发行版：换到了就按那里检测到的补弹一次「添加 Agent」。
export function switchedToDistro(before: string, after: string): boolean {
  return !!after && after !== before;
}

// globalDirName 是一个位置的叫法，「全局目录」的下拉选项和确认框都用它:Windows 本机，或「WSL · 发行版」。
export function globalDirName(distro: string): string {
  const t = messages();
  return distro ? t.paths.wsl(distro) : t.settings.windowsHome;
}
