// 列表顶部的摘要条：作用域页的作用域摘要;更新页的更新列表也用这里的外观。
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import {
  agentNameIn,
  groupStateIn,
  needsReinstall,
  parseScope,
  placeOf,
  scopeLabel,
  shortPath,
  skillScopesIn,
  unmanagedIn,
  viaGroupCount,
  type ScopeKey,
} from "@/lib/model";
import { cn } from "@/lib/utils";
import { offerFetchUnmanaged } from "@/features/fetch/fetch-unmanaged-all";
import { installGroup, uninstallGroup } from "@/features/install/install";
import { reinstall } from "@/features/install/reinstall";
import { Button } from "@/components/ui/button";
import { Icon } from "@/components/app/icons";

// 列表顶部的摘要条(作用域摘要、更新列表)共用的外观。
export const barClass = "a-scope-sum flex flex-col gap-2 rounded-[10px] border border-hairline bg-glass-card px-3 py-2.5 shadow-tile";

export const barRowClass = "a-sum-row flex min-w-0 items-center gap-2.5";

// ScopeSummary 是作用域页顶部的摘要:Agent 在这里一共看得到几个技能、Agent 目录、健康状态、全部重新安装，以及每个组的整组开关。
export function ScopeSummary({ scope, directory, sees }: { scope: ScopeKey; directory: string | undefined; sees: number }) {
  const s = useSnapshot();
  const t = useT().list;
  const installed = skillScopesIn(s, scope);
  const issues = installed.filter(needsReinstall).length;
  const unmanaged = unmanagedIn(s, scope).length;
  // 在项目仓库里的、项目 skills-lock.json 记的归项目仓库管，指向 Agent 目录以外的是作者有意留在自己目录里的，都不算进"还没交给 app 管"的数里。
  const notTaken = unmanagedIn(s, scope).filter((u) => !u.in_repository && !u.in_project_lock && !u.link_outside).length;
  const sc = parseScope(scope);
  return (
    <div className={barClass}>
      <b className="scope-sees text-[13px] font-semibold">{t.scopeSees(agentNameIn(s, sc.agent, placeOf(sc)), sees)}</b>
      <div className={barRowClass}>
        <span className="text-[11px] text-muted-foreground">{t.agentDirectory}</span>
        <span className="max-w-[360px] truncate font-mono text-[11px] select-text">{directory ? shortPath(s, directory) : "…"}</span>
        <span className="grow" />
        <Stat value={installed.length} label={t.statInstalled} />
        {unmanaged > 0 && <Stat value={unmanaged} label={t.statUnmanaged} />}
        <Stat value={issues} label={t.statStale} warn={issues > 0} />
        {issues > 0 && (
          <Button size="sm" onClick={() => reinstall({ scope: sc }, scopeLabel(s, sc))}>
            <Icon name="refresh" />
            {t.reinstallAll}
          </Button>
        )}
      </div>
      {notTaken > 0 && (
        <div className="unmanaged-hint flex items-center gap-1.5 text-[11px] text-muted-foreground">
          <span>{t.notTaken(notTaken)}</span>
          <Button
            variant="link"
            className="fetch-all-unmanaged text-[11px]"
            onClick={() => void offerFetchUnmanaged(s.agents, [sc], { kind: "scope", label: scopeLabel(s, sc) }, false)}
          >
            {t.fetchAll}
          </Button>
        </div>
      )}
      {s.groups.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-[11px] text-muted-foreground">{t.groups}</span>
          {s.groups.map((g) => {
            const state = groupStateIn(s, g, scope);
            const on = state === "on";
            return (
              <button
                type="button"
                key={g.id}
                data-state={state}
                aria-pressed={on}
                className={cn(
                  "gchip inline-flex h-6 items-center gap-1.5 rounded-md border border-border px-[9px] text-[11.5px] text-foreground-secondary transition-colors hover:border-border-strong hover:bg-white/70",
                  on && "border-group-border bg-group-soft font-medium text-[#5b21b6] hover:border-group hover:bg-group-soft",
                )}
                onClick={() => (on ? uninstallGroup(g.id, sc) : installGroup(g.id, sc))}
                title={t.groupChipTitle(g.name, on)}
              >
                <i className={cn("size-[7px] rounded-full border-[1.5px] border-subtle", on && "border-group bg-group")} />
                {g.name}
                <span className={cn("font-mono text-[11px] text-subtle", on && "text-[#7c3aed]")}>
                  {viaGroupCount(s, g, scope)}/{g.members?.length ?? 0}
                </span>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

function Stat({ value, label, warn = false }: { value: number; label: string; warn?: boolean }) {
  return (
    <span className="stat text-xs text-foreground-secondary" data-tone={warn ? "warn" : undefined}>
      <b className={cn("mr-0.5 text-sm", warn && "text-amber-600")}>{value}</b> {label}
    </span>
  );
}
