// 列表顶部的摘要条：作用域页的作用域摘要、项目页的项目摘要;更新页的更新列表也用这里的外观。
import type { RegisteredProject } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import {
  agentName,
  agentNameIn,
  groupStateIn,
  editedHere,
  needsRepair,
  reinstallLevel,
  skillScopeIn,
  parseScope,
  placeOf,
  projectName,
  scopeLabel,
  shortPath,
  skillScopesIn,
  splitIdentical,
  unmanagedIn,
  wslTitle,
  type ScopeKey,
} from "@/lib/model";
import type { Level } from "@/lib/level";
import { cn } from "@/lib/utils";
import { offerFetchProject } from "@/features/fetch/fetch-unmanaged-all";
import { openCopyFromAgent, openCopyFromProject } from "@/features/install/copy";
import { installGroup, uninstallGroup } from "@/features/install/install";
import { reinstall } from "@/features/install/reinstall";
import { Button } from "@/components/ui/button";
import { Icon } from "@/components/ui/icon";
import { WSLBadge } from "@/components/app/parts";
import { Badge } from "@/components/ui/badge";
import { Notice } from "@/components/ui/notice";
import { ui } from "@/lib/ui";
import { ProjectPathDialog } from "@/features/scope/project-path-dialog";
import { nameIndent } from "./rows";

// 列表顶部的摘要条(作用域摘要、更新列表)共用的外观：不是浮起的卡片，直接排在底板上，和下面的列表之间隔一条线。
// 左右内边距由用的地方给：文字要和下面列表里的技能名在同一条线上(作用域页、项目页的行前面有勾选框那一列，更新页没有)。
export const barClass = "a-scope-sum flex flex-col gap-1.5 border-b border-row-line pt-0.5 pb-2.5";

export const barRowClass = "a-sum-row flex min-w-0 items-center gap-2.5";

// ScopeSummary 是作用域页顶部的摘要：这个目录里有几个技能(here:已安装、和库里一样但不是 app 装的、不在技能库里的都算，
// 来自全局的不算，和右栏这个作用域的数字一样;其中各几个与来自全局的几个写在第二行，和下面的上一块一致)、
// Agent 目录、健康状态、全部重新安装，每个组的整组开关，
// 以及照着其他 Agent 装(别的 Agent 装了库里的技能时才有;项目的作用域页上默认只装这个项目)。
export function ScopeSummary({ scope, directory, here, global }: { scope: ScopeKey; directory: string | undefined; here: number; global: number }) {
  const s = useSnapshot();
  const t = useT().list;
  const installed = skillScopesIn(s, scope);
  // 真要重新安装的(文件夹不在了、版本落后)和被手动改过的分开数：被手动改过的是用户自己的修改，不催用户重新安装。
  const broken = installed.filter(needsRepair);
  const issues = broken.length;
  const edited = installed.filter(editedHere).length;
  // 和库里某个技能一样的文件夹就是那个技能，不算"不在技能库里",单独数。
  const split = splitIdentical(unmanagedIn(s, scope));
  const unmanaged = split.unmanaged.length;
  const identical = split.identical.length;
  const sc = parseScope(scope);
  const groups = s.groups;
  const others = s.skillScopes.some((p) => p.scope.agent !== sc.agent) || s.groupInstalls.some((g) => g.scope.agent !== sc.agent);
  const c = useT().copy;
  const agent = agentNameIn(s, sc.agent, placeOf(sc));
  const missingProject = sc.project_id ? s.projects.find((p) => p.id === sc.project_id && p.missing) : undefined;
  return (
    <div className={cn(barClass, nameIndent, "pr-3")}>
      {/* 第一行：标题在左，这一页的动作都在右边排成一排。 */}
      <div className="flex min-w-0 items-center gap-1.5">
        <b className="scope-sees min-w-0 grow truncate text-heading">
          {sc.project_id ? t.scopeSeesProject(agent, projectName(s, sc.project_id), here) : t.scopeSeesGlobal(agent, here)}
        </b>
        {/* 要重新安装的，和被手动改过的(确认里默认不覆盖，勾上才覆盖)都从这里一次处理;只有在 app 外更新到作者新版的不给(该做的是更新)。 */}
        {(issues > 0 || edited > 0) && (
          <Button className="shrink-0" onClick={() => reinstall({ scope: sc }, scopeLabel(s, sc))}>
            <Icon name="refresh" />
            {t.reinstallAll}
          </Button>
        )}
        {others && (
          <Button className="copy-agent-open shrink-0" title={c.openTitle(agentName(s, sc.agent))} onClick={() => openCopyFromAgent(sc.agent, placeOf(sc))}>
            <Icon name="plus" />
            {c.open}
          </Button>
        )}
      </div>
      {/* 第二行：目录和各个数字从左到右写成一行，要重新安装的用「注意」的标签标出。 */}
      <div className={cn(barRowClass, "gap-x-3 text-caption text-muted-foreground")}>
        <span className="flex min-w-0 items-center gap-1.5">
          {t.agentDirectory}
          <WSLBadge path={directory} />
          <span className="max-w-[360px] truncate font-mono text-foreground-secondary select-text" title={wslTitle(directory)}>
            {directory ? shortPath(s, directory) : "…"}
          </span>
        </span>
        <Stat value={installed.length} label={t.statInstalled} />
        {identical > 0 && <Stat value={identical} label={t.statIdentical} />}
        {unmanaged > 0 && <Stat value={unmanaged} label={t.statNotInLibrary} />}
        {global > 0 && <Stat value={global} label={t.statGlobal} />}
        {issues > 0 && <Stat value={issues} label={t.statStale} level={reinstallLevel(broken)} />}
        {edited > 0 && <Stat value={edited} label={t.statEdited} level="info" title={t.statEditedTitle} />}
      </div>
      {missingProject && <ProjectMissingNotice project={missingProject} />}
      {s.groups.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="shrink-0 text-caption text-muted-foreground">{t.groups}</span>
          {groups.map((g) => {
            const state = groupStateIn(s, g, scope);
            const on = state === "on";
            const size = g.members?.length ?? 0;
            // 组在这里的情况直接写出来：组安装了写「已组安装」,有成员没装上(被占用时跳过)时写出装上了几个;
            // 没有组安装时写成员里装在这里的有几个(直接安装的也算)。
            const here = (g.members ?? []).filter((id) => !!skillScopeIn(s, id, scope)).length;
            return (
              <button
                type="button"
                key={g.id}
                data-state={state}
                aria-pressed={on}
                className={cn(
                  "gchip inline-flex h-[22px] shrink-0 items-center gap-1.5 rounded-control border border-tag-border bg-tag px-2 text-caption text-foreground-secondary transition-colors hover:bg-fill",
                  on && "text-foreground",
                )}
                onClick={() => (on ? uninstallGroup(g.id, sc) : installGroup(g.id, sc))}
                title={t.groupChipTitle(g.name, on)}
              >
                <i className={cn("size-[7px] rounded-full border-[1.5px] border-muted-foreground", on && "border-primary bg-primary")} />
                {g.name}
                <span className="gchip-state text-caption text-muted-foreground">{on ? t.groupInstalledHere(here, size) : t.groupMembersHere(here, size)}</span>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

// ProjectSummary 是项目页顶部的摘要：项目里一共有几个技能(几个 Agent 都装了的只算一个，不是 app 装的也算，和右栏这个项目的数字一样)与项目目录，
// 其中不是 app 装的、不在技能库里的几个与全部入库(范围和登记项目后弹出的那次一样，是各 Agent 在这个项目里;和库里一样、只是不是 app 装的也算),
// 以及照着其他项目装(别的已登记项目里装了库里的技能时才有)。
export function ProjectSummary({ project, total, unmanaged }: { project: RegisteredProject; total: number; unmanaged: number }) {
  const s = useSnapshot();
  const t = useT().list;
  const c = useT().copy;
  const others = s.skillScopes.some((p) => !!p.scope.project_id && p.scope.project_id !== project.id);
  const name = projectName(s, project.id);
  // 不是库装的文件夹都算(和库里某个技能一样的也算：入库只记下装在这里):有就在顶部放一次入库的入口。
  const notOurs = new Set(s.unmanaged.filter((u) => placeOf(u.scope) === project.id).map((u) => u.folder));
  const identical = new Set(s.unmanaged.filter((u) => placeOf(u.scope) === project.id && !!u.skill_id).map((u) => u.folder)).size;
  return (
    <div className={cn(barClass, nameIndent, "pr-3")}>
      <div className="flex min-w-0 items-center gap-1.5">
        <b className="project-sum min-w-0 grow truncate text-heading">{t.projectHas(name, total)}</b>
        {notOurs.size > 0 && (
          <Button className="fetch-all-unmanaged shrink-0" title={t.fetchAllTitle} onClick={() => void offerFetchProject(s.agents, project, false)}>
            {t.fetchAll}
          </Button>
        )}
        {others && (
          <Button className="copy-project-open shrink-0" title={c.projectOpenTitle(name)} onClick={() => openCopyFromProject(project.id)}>
            <Icon name="plus" />
            {c.projectOpen}
          </Button>
        )}
      </div>
      <div className={cn(barRowClass, "gap-x-3 text-caption text-muted-foreground")}>
        <span className="flex min-w-0 items-center gap-1.5">
          {t.projectDirectory}
          <WSLBadge path={project.path} />
          <span className="max-w-[360px] truncate font-mono text-foreground-secondary select-text" title={wslTitle(project.path)}>
            {shortPath(s, project.path)}
          </span>
        </span>
        {identical > 0 && <Stat value={identical} label={t.statIdentical} />}
        {unmanaged > 0 && <Stat value={unmanaged} label={t.statNotInLibrary} />}
      </div>
      <ProjectMissingNotice project={project} />
    </div>
  );
}

// ProjectMissingNotice 是项目不在这台机器上时项目页、项目里各作用域页顶部的提醒：说清项目目录不存在、装在这里的现在用不上,
// 旁边就能改项目位置(和右栏项目行上的「改项目位置」是同一个弹窗)。
export function ProjectMissingNotice({ project }: { project: RegisteredProject }) {
  const s = useSnapshot();
  const t = useT().list;
  if (!project.missing) return null;
  const where = wslTitle(project.path) ?? shortPath(s, project.path);
  return (
    <Notice
      alert
      className="project-missing mt-1"
      action={
        <Button
          className="relocate-project shrink-0"
          onClick={() => void ui.open((close) => <ProjectPathDialog home={s.info.home_dir} project={project} onClose={close} />)}
        >
          {t.projectMissingRelocate}
        </Button>
      }
    >
      {t.projectMissing(where)}
    </Notice>
  );
}

// level 是这一项要提醒时的级别(需重新安装的按最重的那一处)。
function Stat({ value, label, level, title }: { value: number; label: string; level?: Level; title?: string }) {
  return (
    <span className="stat shrink-0 whitespace-nowrap" data-tone={level} title={title}>
      {level ? (
        <Badge tone={level}>
          {value} {label}
        </Badge>
      ) : (
        <>
          <b className="font-semibold text-foreground-secondary tabular-nums">{value}</b> {label}
        </>
      )}
    </span>
  );
}
