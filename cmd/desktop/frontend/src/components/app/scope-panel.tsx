// 右栏：作用域——全局与每个已登记项目下，直接列出装有技能的与检测到的 Agent,勾选了显示但没检测到的折进"其他 N 个 Agent";
// 项目行折叠时用小图标显示在用的 Agent。点一行打开这个作用域的页面(Agent 在那里看得到什么),再点一次回到所有技能。
// 项目里共用一个目录的几个 Agent 合成一行，叫那几个 Agent 的名字，点选的是其中代表的作用域。
// 作用域行接受拖放：技能 = 直接安装，组 = 组安装，集合 = 把集合里的技能逐个直接安装;拖动中按指针轨迹预判要去的折叠项目或"其他 Agent",
// 指针还没到就展开，拖完收回。
import { useEffect, useRef, useState } from "react";
import { useShallow } from "zustand/react/shallow";
import type { Agent, RegisteredProject } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import {
  agentNameIn,
  agentsIn,
  agentsUsedIn,
  mergeProjectPlaces,
  needsReinstall,
  parseScope,
  placeOf,
  projectFolderNote,
  projectName,
  sameMergedScope,
  scopeKey,
  scopeOf,
  shortPath,
  skillScopesIn,
  type Place,
} from "@/lib/model";
import { useUI } from "@/lib/ui";
import { cn } from "@/lib/utils";
import { openScope, toggleMoreAgents, toggleProject, useCurrentPage, useView } from "@/lib/view";
import { ProjectPathDialog } from "@/features/scope/project-path-dialog";
import { unregisterProject } from "@/features/scope/projects";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { alreadyInScope, dropProps, useDnD } from "./dnd";
import { Icon } from "./icons";
import { rightClass } from "./layout";
import { NavCount, navItem } from "./nav";
import { PlaceAgentMark } from "./parts";

export function ScopePanel() {
  const { expanded, moreAgents } = useView(useShallow((v) => ({ expanded: v.expanded, moreAgents: v.moreAgents })));
  const page = useCurrentPage();
  const scope = page.kind === "scope" ? page.scope : null;
  const s = useSnapshot();
  const ui = useUI();
  const t = useT().scopes;
  const tl = useT().layout;
  const dnd = useDnD();
  const selected = scope ? parseScope(scope) : null;
  // isSelected:这一行是不是选中的作用域;项目里合成的一行，选中共用的任何一个都算。
  const isSelected = (a: Agent, place: Place) => !!selected && sameMergedScope(s, selected, scopeOf(a.code, place));
  const rail = useRef<HTMLElement>(null);

  // aimed:这次拖动中预判展开过的项目("p" + ID)与"其他 Agent"("m" + 位置);拖动中不收回，免得列表在指针下跳动。
  const [aimed, setAimed] = useState<Set<string>>(new Set());
  const dragging = !!dnd.item;
  useEffect(() => {
    if (!dragging || !rail.current) return;
    const stop = trackAim(rail.current, (key) => setAimed((set) => (set.has(key) ? set : new Set(set).add(key))));
    return () => {
      stop();
      setAimed(new Set());
    };
  }, [dragging]);
  const aimProps = (key: string, open: boolean) => (dragging && !open ? { "data-aim": key } : {});

  const scopeRow = (agent: Agent, place: Place) => {
    const key = scopeKey(scopeOf(agent.code, place));
    const installed = skillScopesIn(s, key);
    const issues = installed.filter(needsReinstall).length;
    const already = !!dnd.item && alreadyInScope(s, dnd.item, key);
    return (
      <li key={key}>
        <button
          type="button"
          className={cn(navItem({ kind: place ? "nested" : "tree" }), "sub")}
          data-active={isSelected(agent, place)}
          data-already={already ? tl.alreadyThere : undefined}
          onClick={() => openScope(isSelected(agent, place) && scope ? scope : key)}
          data-drop-scope={key}
          {...dropProps(!!dnd.item && !already, () => dnd.dropOnScope(key))}
        >
          <PlaceAgentMark code={agent.code} place={place} />
          <span className="grow truncate" title={projectFolderNote(s, agent.code, place)}>
            {agentNameIn(s, agent.code, place)}
          </span>
          {issues > 0 && (
            <i className="size-1.5 shrink-0 rounded-full bg-warn" title={t.issues(issues)} />
          )}
          <NavCount value={installed.length} accent />
        </button>
      </li>
    );
  };

  // 正被选中的作用域即使折在"其他"里也直接列出，否则看不到选中态。
  const agentRows = (place: Place) => {
    const { open, folded } = agentsIn(s, place);
    const shown = [...open, ...folded.filter((a) => isSelected(a, place))];
    const rest = folded.filter((a) => !isSelected(a, place));
    const more = moreAgents.has(place) || aimed.has(`m${place}`);
    return (
      <>
        {shown.map((a) => scopeRow(a, place))}
        {more && rest.map((a) => scopeRow(a, place))}
        {rest.length > 0 && (
          <li>
            <button
              type="button"
              className={cn(navItem({ kind: place ? "nested" : "tree" }), "sub more text-xs text-muted-foreground")}
              onClick={() => toggleMoreAgents(place)}
              {...aimProps(`m${place}`, more)}
            >
              <Icon name="chev" className={cn("size-2.5 transition-transform", more ? "-rotate-90" : "rotate-90")} />
              <span className="grow">{more ? t.collapse : t.more(rest.length)}</span>
            </button>
          </li>
        )}
        {shown.length === 0 && rest.length === 0 && (
          <li className="px-2 py-1 text-[11.5px] text-muted-foreground">{t.noAgents}</li>
        )}
      </>
    );
  };

  const register = () => ui.open((close) => <ProjectPathDialog home={s.info.home_dir} onClose={close} />);
  const relocate = (project: RegisteredProject) =>
    ui.open((close) => <ProjectPathDialog home={s.info.home_dir} project={project} onClose={close} />);

  return (
    <aside ref={rail} className={rightClass}>
      <RailHead icon="globe" label={t.global} />
      <ul className={listClass}>{agentRows(0)}</ul>
      <RailHead icon="folder" label={t.projects}>
        <Button
          variant="ghost"
          size="icon-sm"
          className="opacity-0 group-hover/sec:opacity-100 focus-visible:opacity-100"
          title={t.register}
          onClick={register}
        >
          <Icon name="plus" />
        </Button>
      </RailHead>
      <ul className={listClass}>
        {s.projects.map((p) => {
          const open = expanded.has(p.id) || aimed.has(`p${p.id}`);
          const count = mergeProjectPlaces(s, s.skillScopes.filter((x) => placeOf(x.scope) === p.id)).length;
          const current = !!selected && placeOf(selected) === p.id;
          return (
            <li key={p.id}>
              <div
                role="button"
                tabIndex={0}
                aria-expanded={open}
                className={cn(navItem({ kind: "tree" }), "proj gap-1.5", current && "font-semibold text-foreground")}
                onClick={() => toggleProject(p.id)}
                title={p.missing ? t.missingTitle(shortPath(s, p.path)) : shortPath(s, p.path)}
                {...aimProps(`p${p.id}`, open)}
              >
                <Icon name="chev" className={cn("size-2.5 transition-transform", open && "rotate-90")} />
                <Icon name="folder" className="size-[13px]" />
                <span className="grow truncate">{projectName(s, p.id)}</span>
                {p.missing && (
                  <Badge tone="warn" title={t.missingBadgeTitle}>
                    {t.missing}
                  </Badge>
                )}
                {!open && (
                  <span className="inline-flex items-center gap-[3px] group-hover:hidden">
                    {agentsUsedIn(s, p.id).map((a) => (
                      <PlaceAgentMark key={a.code} code={a.code} place={p.id} className="size-3" />
                    ))}
                  </span>
                )}
                <span className="row-actions hidden gap-0.5 group-hover:inline-flex">
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    title={t.relocateTitle}
                    onClick={(e) => {
                      e.stopPropagation();
                      void relocate(p);
                    }}
                  >
                    <Icon name="edit" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    title={t.unregisterTitle}
                    onClick={(e) => {
                      e.stopPropagation();
                      void unregisterProject(p);
                    }}
                  >
                    <Icon name="close" />
                  </Button>
                </span>
                <span className="group-hover:hidden">
                  <NavCount value={count} />
                </span>
              </div>
              <div
                className={cn(
                  "grid transition-[grid-template-rows] duration-200 ease-out motion-reduce:transition-none",
                  open ? "grid-rows-[1fr]" : "grid-rows-[0fr]",
                )}
                inert={!open}
              >
                <ul className="flex min-h-0 flex-col gap-0.5 overflow-hidden pt-0.5 pl-[18px]">{agentRows(p.id)}</ul>
              </div>
            </li>
          );
        })}
      </ul>
      {s.projects.length === 0 && <Hint className="mt-1 px-6">{t.noProjects}</Hint>}
    </aside>
  );
}

// trackAim 在拖动中按指针轨迹预判落点：用最近 100ms 的速度外推 250ms,从栏外往里移时取进入右栏那一刻的高度;
// 落点稳定落在同一个折叠目标(带 data-aim 的行)上 80ms 就展开它。快速扫过一串行时落点一直在变，不会展开;
// 慢下来时落点就是指针所在处，相当于停留展开。
function trackAim(rail: HTMLElement, onAim: (key: string) => void): () => void {
  const samples: { x: number; y: number; t: number }[] = [];
  let target: string | null = null;
  let since = 0;
  const onOver = (e: DragEvent) => {
    const t = performance.now();
    samples.push({ x: e.clientX, y: e.clientY, t });
    while (samples.length > 1 && t - samples[0].t > 100) samples.shift();
    const first = samples[0];
    const dt = Math.max(t - first.t, 1);
    let x = e.clientX + ((e.clientX - first.x) / dt) * 250;
    let y = e.clientY + ((e.clientY - first.y) / dt) * 250;
    const left = rail.getBoundingClientRect().left;
    if (e.clientX < left && x > left) {
      y = e.clientY + ((y - e.clientY) * (left - e.clientX)) / (x - e.clientX);
      x = left;
    }
    const hit = aimAt(rail, x, y);
    if (hit !== target) {
      target = hit;
      since = t;
    } else if (hit && t - since >= 80) {
      onAim(hit);
    }
  };
  document.addEventListener("dragover", onOver);
  return () => document.removeEventListener("dragover", onOver);
}

// aimAt 找出 (x, y) 所在高度的折叠目标;收起的项目里的行虽然还在页面上，但被裁掉看不见，不算。
function aimAt(rail: HTMLElement, x: number, y: number): string | null {
  if (x < rail.getBoundingClientRect().left) return null;
  for (const el of rail.querySelectorAll<HTMLElement>("[data-aim]")) {
    if (el.closest("[inert]")) continue;
    const r = el.getBoundingClientRect();
    if (y >= r.top - 3 && y < r.bottom + 3) return el.dataset.aim!;
  }
  return null;
}

// 行的左缘对齐分节标题的文字：标题是 图标 + 名称，行从名称下方开始。
const listClass = "mt-0.5 flex flex-col gap-0.5 pl-4";

function RailHead({ icon, label, children }: { icon: "globe" | "folder"; label: string; children?: React.ReactNode }) {
  return (
    <div className="nav-sec group/sec flex h-[26px] items-center justify-between pr-0.5 pl-2 text-[11px] font-semibold text-muted-foreground not-first:mt-2.5 first:mt-1">
      <span className="inline-flex items-center gap-[5px]">
        <Icon name={icon} className="size-[11px]" />
        {label}
      </span>
      {children}
    </div>
  );
}
