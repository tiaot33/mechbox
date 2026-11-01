// 选装到哪里：先选地方(全局 / 项目),再勾选装到哪几个 Agent。详情里的"安装到…"和勾选后操作栏的"安装到…"共用这两个选择，
// 这个地方在用的 Agent 排在前面，其余只列勾选显示的;项目里共用一个目录的几个 Agent 合成一个选项。
import { useState } from "react";
import type { Agent, AgentCode, Scope, SkillID } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import { agentNameIn, agentShown, agentsUsedIn, mergeAgentsExcept, projectName, scopeKey, scopeOf, skillScopeIn, type Place } from "@/lib/model";
import { Modal, ui } from "@/lib/ui";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Field, Hint } from "@/components/ui/field";
import { NativeSelect } from "@/components/ui/native-select";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Icon } from "@/components/app/icons";
import { installSkills } from "./install";

type Initial = { place?: Place; agent?: AgentCode };

// useInstallTarget 是选到的地方与勾上的 Agent:available 是这个 Agent 能不能选(要装的都已直接安装在那里的不能选)。
// 没动过勾选时默认勾上这个地方在用、能选的 Agent,以及 initial 里这一页的 Agent;换地方后回到默认。
// agents 只含选项里能选的，按选项的顺序排;selectable 是这个地方有没有能选的 Agent。
export function useInstallTarget(available: (scope: Scope) => boolean, initial: Initial = {}) {
  const s = useSnapshot();
  const [place, setPlaceState] = useState<Place>(initial.place ?? 0);
  const [picked, setPicked] = useState<AgentCode[] | null>(null);
  const used = agentsUsedIn(s, place);
  const rest = mergeAgentsExcept(s, place, s.agents.filter((a) => agentShown(s.agentsShown, a)), used);
  const can = (code: AgentCode) => available(scopeOf(code, place));
  const options = [...used, ...rest];
  const defaults = [...used.map((a) => a.code), ...(place === (initial.place ?? 0) && initial.agent ? [initial.agent] : [])];
  const chosen = picked ?? defaults;
  const agents = options.map((a) => a.code).filter((code) => chosen.includes(code) && can(code));
  const setPlace = (p: Place) => {
    setPlaceState(p);
    setPicked(null);
  };
  const toggle = (code: AgentCode) => setPicked(agents.includes(code) ? agents.filter((c) => c !== code) : [...agents, code]);
  const scopes = agents.map((code) => scopeOf(code, place));
  return { place, setPlace, agents, toggle, scopes, used, rest, can, options, selectable: options.some((a) => can(a.code)) };
}

export type InstallTarget = ReturnType<typeof useInstallTarget>;

export function PlaceSelect({ target }: { target: InstallTarget }) {
  const s = useSnapshot();
  const t = useT();
  return (
    <NativeSelect value={target.place} onChange={(e) => target.setPlace(Number(e.target.value))}>
      <option value={0}>{t.model.global}</option>
      {s.projects.map((p) => (
        <option key={p.id} value={p.id}>
          {projectName(s, p.id)}
        </option>
      ))}
    </NativeSelect>
  );
}

// AgentPicker 是勾选装到哪几个 Agent:收起时写出勾上的全部 Agent 的名字，展开是可勾选的列表;
// 不能选的灰掉，旁边写 taken(详情里是"已装",操作栏里是"都已直接安装")。
export function AgentPicker({ target, taken }: { target: InstallTarget; taken: string }) {
  const s = useSnapshot();
  const t = useT();
  const [open, setOpen] = useState(false);
  const { place, used, rest, can } = target;
  const names = target.agents.map((code) => agentNameIn(s, code, place));
  const heading = (text: string) => <div className="px-2 pt-1.5 pb-0.5 text-[11px] font-medium text-muted-foreground">{text}</div>;
  const row = (a: Agent) => (
    <label
      key={a.code}
      data-agent={a.code}
      className={cn(
        "agent-option flex items-center gap-2 rounded-md px-2 py-1 text-xs",
        can(a.code) ? "cursor-pointer hover:bg-black/5" : "text-subtle",
      )}
    >
      <input
        type="checkbox"
        className="shrink-0 accent-primary"
        checked={target.agents.includes(a.code)}
        disabled={!can(a.code)}
        onChange={() => target.toggle(a.code)}
      />
      <span className="min-w-0 grow truncate">{agentNameIn(s, a.code, place)}</span>
      {!can(a.code) && <span className="shrink-0 text-[11px]">{taken}</span>}
    </label>
  );
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          title={names.length ? t.model.agentNames(names) : undefined}
          className="agent-pick inline-flex min-h-[26px] max-w-[260px] min-w-0 items-center gap-1 rounded-[7px] border border-border bg-white px-1.5 py-[3px] text-left text-xs text-foreground outline-none focus-visible:ring-3 focus-visible:ring-primary-soft"
        >
          <span className={cn("agent-pick-names min-w-0 grow", !names.length && "text-muted-foreground")}>
            {names.length ? t.model.agentNames(names) : t.drawer.pickAgents}
          </span>
          <Icon name="down" className="size-3 shrink-0 text-muted-foreground" />
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="agent-menu z-[60] max-h-72 w-60 overflow-y-auto">
        {used.length > 0 && (
          <>
            {heading(t.drawer.usedHere)}
            {used.map(row)}
          </>
        )}
        {rest.length > 0 && (
          <>
            {heading(used.length ? t.drawer.otherAgents : t.drawer.agent)}
            {rest.map(row)}
          </>
        )}
      </PopoverContent>
    </Popover>
  );
}

// installPicked 是勾选后操作栏的"安装到…":先选装到哪里，再把勾选的技能一起装到勾上的各个 Agent。initial 是默认选的地方
// 与 Agent(作用域页是这个作用域，项目页是这个项目)。
export async function installPicked(ids: SkillID[], initial: Initial = {}) {
  const scopes = await ui.open<Scope[]>((close) => <InstallToDialog ids={ids} initial={initial} close={close} />);
  if (scopes?.length) await installSkills(ids, scopes);
}

function InstallToDialog({ ids, initial, close }: { ids: SkillID[]; initial: Initial; close: (scopes?: Scope[]) => void }) {
  const s = useSnapshot();
  const t = useT();
  const target = useInstallTarget((sc) => !ids.every((id) => skillScopeIn(s, id, scopeKey(sc))?.direct), initial);
  const placeName = target.place ? projectName(s, target.place) : t.model.global;
  return (
    <Modal
      title={t.list.pickInstallTitle(ids.length)}
      onClose={() => close()}
      footer={
        <>
          <Button onClick={() => close()}>{t.common.cancel}</Button>
          <Button variant="primary" disabled={!target.agents.length} onClick={() => close(target.scopes)}>
            {t.actions.install}
          </Button>
        </>
      }
    >
      <div className="install-picked flex flex-wrap items-end gap-3">
        <Field label={t.list.pickPlace}>
          <PlaceSelect target={target} />
        </Field>
        {target.options.length > 0 && (
          <Field label={t.drawer.agent}>
            <AgentPicker target={target} taken={t.list.pickAllDirect} />
          </Field>
        )}
      </div>
      {!target.selectable && (
        <Hint className="mt-2">{target.options.length ? t.list.pickAllInstalled(placeName) : t.drawer.noAgents(placeName)}</Hint>
      )}
      <Hint className="mt-2">{t.list.pickInstallHint}</Hint>
    </Modal>
  );
}
