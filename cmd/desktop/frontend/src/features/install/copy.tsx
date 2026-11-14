// 照着装：照着另一个 Agent 或另一个已登记项目，把那里装的库里技能一次装过来。
// - 照着另一个 Agent(作用域页):把它在全局和各已登记项目里装的装到这个 Agent 的同一处，按地方分开列出;
//   从项目的作用域页打开时默认只勾这个项目(见 consent.ts 的 copyPlaceByDefault)。
// - 照着另一个项目(项目页):把各 Agent 在那个项目里装的装到这个项目里的同一个 Agent,按 Agent 分开列出;全局不在范围内。
// 只装一次、只加不减，装哪些见 model.ts 的 copyFromAgent / copyFromProject;默认勾选与覆盖见 consent.ts。
// - 先逐个看安装计划：被占用的不能勾并写出原因;有不是库装的同名文件夹的默认不勾，勾上才覆盖。
//   组安装默认勾上，组员里有这种文件夹的默认跳过，勾上"覆盖"才覆盖。
// - 项目不在这台机器上的那一处跳过并说明;原来那里改过的技能、未纳管技能，确认时说出来。
// - 一项装不上不影响其余的;全部装上时只提示装了几个，有没装上的才列出结果。
import { useEffect, useRef, useState } from "react";
import { LibraryService } from "@bindings/cmd/desktop";
import { GroupMemberOutcome, InstallPlanStatus, type AgentCode, type Group, type ProjectID, type Skill } from "@bindings/internal/library";
import { copyable, copyDefaults, copyOverwriteCount, copyOverwrites, copyPlaceByDefault, type CopyPlanned } from "@/lib/consent";
import { describeError } from "@/lib/errors";
import { messages, useT } from "@/lib/i18n";
import { currentSnapshot, toLibraryError, type LibraryError, type Snapshot } from "@/lib/library";
import {
  agentName,
  agentNameIn,
  copyFromAgent,
  copyFromProject,
  copyItems,
  copyProjectSources,
  copySources,
  placeOf,
  projectName,
  scopeKey,
  scopeLabel,
  shortPath,
  sourceLabel,
  uncopiedProjectUnmanaged,
  uncopiedUnmanaged,
  type CopyPlace,
  type Place,
  type UncopiedUnmanaged,
} from "@/lib/model";
import { Modal, ui } from "@/lib/ui";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { ListBox, ListRow } from "@/components/ui/list";
import { NativeSelect } from "@/components/ui/native-select";
import { Notice } from "@/components/ui/notice";
import { memberRows, showResults, type ResultRow } from "@/components/app/results";
import { occupiedReason } from "./install";

// CopyKind 是一种照装：照着谁(sources 的 key)、装到的那一处的名字(toName)、要装的各处、各处的标题与确认框里另外说的话。
// 文案都在调用时取，跟随当前界面语言。
interface CopyKind {
  title: () => string;
  toName: string;
  sources: { key: string; name: string }[];
  lead: () => string;
  once: (from: string) => string;
  places: (from: string) => CopyPlace[];
  heading: (p: CopyPlace) => { name: string; detail?: string };
  // left 是带不过来的未纳管技能:unmanaged 入库以后才能照着装，repo 归项目仓库管、随项目仓库走。
  left: (from: string) => UncopiedUnmanaged;
  // byDefault 是这一处的各项打开时默认勾不勾。
  byDefault: (p: CopyPlace) => boolean;
}

// openCopyFromAgent 是作用域页的"照着其他 Agent 装…",at 是从哪个作用域页打开的(全局为 0);
// 从项目的作用域页打开时，这个项目排在最前、默认只勾它。没有可以照着装的 Agent 时只告诉一声。
export function openCopyFromAgent(to: AgentCode, at: Place) {
  const s = currentSnapshot();
  const sources = copySources(s, to, at);
  const toName = agentName(s, to);
  if (!sources.length) {
    ui.toast(messages().copy.nothing(toName));
    return;
  }
  openCopy(s, {
    title: () => messages().copy.title(toName),
    toName,
    sources: sources.map((a) => ({ key: a.code, name: a.name })),
    lead: () => messages().copy.lead(toName),
    once: (from) => messages().copy.once(from, toName),
    places: (from) => {
      const all = copyFromAgent(s, from as AgentCode, to);
      const here = (p: CopyPlace) => !!at && placeOf(p.to) === at;
      return [...all.filter(here), ...all.filter((p) => !here(p))];
    },
    heading: (p) => ({
      name: placeOf(p.to) ? projectName(s, placeOf(p.to)) : messages().model.global,
      detail: agentNameIn(s, p.to.agent, placeOf(p.to)),
    }),
    left: (from) => uncopiedUnmanaged(s, from as AgentCode, to),
    byDefault: (p) => copyPlaceByDefault(at, placeOf(p.to)),
  });
}

// openCopyFromProject 是项目页的"照着其他项目装…":这个项目不在这台机器上、没有可以照着装的项目时只告诉一声。
export function openCopyFromProject(to: ProjectID) {
  const s = currentSnapshot();
  const t = messages().copy;
  const toName = projectName(s, to);
  if (s.projects.find((p) => p.id === to)?.missing) {
    ui.toast(t.projectMissing(toName));
    return;
  }
  const sources = copyProjectSources(s, to);
  if (!sources.length) {
    ui.toast(t.projectNothing(toName));
    return;
  }
  openCopy(s, {
    title: () => messages().copy.projectTitle(toName),
    toName,
    sources: sources.map((p) => ({ key: String(p.id), name: projectName(s, p.id) })),
    lead: () => messages().copy.projectLead(toName),
    once: (from) => messages().copy.projectOnce(from, toName),
    places: (from) => copyFromProject(s, Number(from), to),
    heading: (p) => ({ name: agentNameIn(s, p.to.agent, to) }),
    left: (from) => uncopiedProjectUnmanaged(s, Number(from), to),
    byDefault: () => true,
  });
}

function openCopy(s: Snapshot, kind: CopyKind) {
  void ui.open((close) => <CopyDialog s={s} kind={kind} close={close} />);
}

// 勾选的 key:一处按装到的作用域区分，再加技能或组。
const skillKey = (p: CopyPlace, id: number) => `s:${scopeKey(p.to)}:${id}`;
const groupKey = (p: CopyPlace, id: number) => `g:${scopeKey(p.to)}:${id}`;
const placeKeys = (p: CopyPlace) => [...p.groups.map((g) => groupKey(p, g.id)), ...p.skills.map((x) => skillKey(p, x.id))];

// Planned 是一项的安装计划(见 consent.ts 的 CopyPlanned),读不出时的错误是技能库错误。Plans 按 skillKey / groupKey 存。
type Planned = Exclude<CopyPlanned, { error: unknown }> | { error: LibraryError };
type Plans = Map<string, Planned>;

// CopyDialog 用打开时的快照算要装什么，不随之后的重读变：确认框开着时重读(如自动检查更新)会重算安装计划、
// 把用户改过的勾选重置掉。
function CopyDialog({ s, kind, close }: { s: Snapshot; kind: CopyKind; close: () => void }) {
  const { copy: t, common } = useT();
  const [from, setFrom] = useState(kind.sources[0].key);
  const [plans, setPlans] = useState<Plans | null>(null);
  const [checked, setChecked] = useState<Set<string>>(new Set());
  const [overwrite, setOverwrite] = useState<Set<string>>(new Set());
  const places = kind.places(from);
  const left = kind.left(from);
  const fromName = kind.sources.find((x) => x.key === from)?.name ?? from;

  useEffect(() => {
    let live = true;
    setPlans(null);
    const open = kind.places(from).filter((p) => !p.missing);
    const byDefault = new Set(open.filter(kind.byDefault).flatMap(placeKeys));
    const calls = open.flatMap((p) => [
      ...p.skills.map((x) => {
        const call = LibraryService.PlanInstall(x.id, p.to);
        return { key: skillKey(p, x.id), call, planned: call.then((plan): Planned => ({ plan })) };
      }),
      ...p.groups.map((g) => {
        const call = LibraryService.PlanGroupInstall(g.id, p.to);
        return { key: groupKey(p, g.id), call, planned: call.then((v): Planned => ({ members: v.members ?? [] })) };
      }),
    ]);
    void Promise.all(
      calls.map(({ key, planned }) => planned.then((v): [string, Planned] => [key, v], (e): [string, Planned] => [key, { error: toLibraryError(e) }])),
    ).then((entries) => {
      if (!live) return;
      const defaults = copyDefaults(entries.filter(([key]) => byDefault.has(key)));
      setPlans(new Map(entries));
      setChecked(defaults.checked);
      setOverwrite(defaults.overwrite);
    });
    return () => {
      live = false;
      calls.forEach(({ call }) => call.cancel());
    };
  }, [kind, from]);

  const toggle = (set: Set<string>, key: string) => {
    const next = new Set(set);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    return next;
  };
  const skillCount = [...checked].filter((k) => k.startsWith("s:")).length;
  const groupCount = checked.size - skillCount;
  // edited 只说勾上的项里会装过来的改过的技能：直接安装勾上了，或随勾上的组安装装过来。
  const coming = (p: CopyPlace, id: number) =>
    checked.has(skillKey(p, id)) || p.groups.some((g) => checked.has(groupKey(p, g.id)) && (g.members ?? []).includes(id));
  const edited = [...new Set(places.flatMap((p) => p.edited.filter((x) => coming(p, x.id)).map((x) => x.name)))];
  const newer = [...new Set(places.flatMap((p) => p.newer.filter((x) => coming(p, x.id)).map((x) => x.name)))];
  const overwrites = plans ? copyOverwriteCount(plans, checked, overwrite) : 0;
  // togglePlace 是一处标题上的勾选：有勾上的就全部取消;一个都没勾时按 copyDefaults 整处勾上(不管这一处打开时默认勾不勾，
  // 不替用户勾上要覆盖的)。
  const togglePlace = (keys: string[]) =>
    setChecked((set) => {
      const next = new Set(set);
      const any = keys.some((k) => next.has(k));
      const entries = keys.flatMap((k): [string, Planned][] => {
        const v = plans?.get(k);
        return v ? [[k, v]] : [];
      });
      const defaults = copyDefaults(entries).checked;
      for (const k of keys) {
        if (any) next.delete(k);
        else if (defaults.has(k)) next.add(k);
      }
      return next;
    });

  const submit = () => {
    if (!plans) return;
    close();
    // 结果只列用户这次要装的那几处：默认勾的，和用户勾了东西的;其余各处装不了、读不出计划的不算没装上。
    const meant = places.filter((p) => kind.byDefault(p) || placeKeys(p).some((k) => checked.has(k)));
    void runCopy(s, fromName, kind.toName, meant, plans, checked, overwrite);
  };

  return (
    <Modal
      title={kind.title()}
      wide
      onClose={close}
      footer={
        <>
          <span className="grow" />
          <Button onClick={close}>{common.cancel}</Button>
          <Button className="copy-confirm" variant="primary" disabled={!plans || checked.size === 0} onClick={submit} autoFocus>
            {t.confirm(skillCount, groupCount, overwrites)}
          </Button>
        </>
      }
    >
      <div className="flex items-center gap-2 text-[13px]">
        <span className="shrink-0 text-muted-foreground">{t.from}</span>
        <NativeSelect className="copy-from" value={from} onChange={(e) => setFrom(e.target.value)}>
          {kind.sources.map((x) => (
            <option key={x.key} value={x.key}>
              {x.name}
            </option>
          ))}
        </NativeSelect>
        <span className="min-w-0 truncate font-semibold">{kind.lead()}</span>
      </div>
      <Hint>{kind.once(fromName)}</Hint>
      <ListBox scroll className="copy-places">
        {places.map((p) => (
          <PlaceRows
            key={scopeKey(p.to)}
            s={s}
            heading={kind.heading(p)}
            place={p}
            plans={plans}
            checked={checked}
            overwrite={overwrite}
            onToggle={(key) => setChecked((set) => toggle(set, key))}
            onOverwrite={(key) => setOverwrite((set) => toggle(set, key))}
            onTogglePlace={togglePlace}
          />
        ))}
      </ListBox>
      {!plans && <Hint>{t.checking}</Hint>}
      {edited.length > 0 && (
        <Notice tone="warn" className="copy-edited">
          {t.edited(fromName, kind.toName, edited)}
        </Notice>
      )}
      {newer.length > 0 && (
        <Notice tone="warn" className="copy-newer">
          {t.newer(fromName, kind.toName, newer)}
        </Notice>
      )}
      {left.unmanaged.length > 0 && <Notice className="copy-unmanaged">{t.unmanaged(fromName, left.unmanaged.length)}</Notice>}
      {left.repo.length > 0 && <Notice className="copy-repo">{t.repoOwned(fromName, left.repo.length)}</Notice>}
    </Modal>
  );
}

// PlaceRows 是一处的各项：先是组安装，再按来源分组列出直接安装的技能。
function PlaceRows({
  s,
  heading,
  place: p,
  plans,
  checked,
  overwrite,
  onToggle,
  onOverwrite,
  onTogglePlace,
}: {
  s: Snapshot;
  heading: { name: string; detail?: string };
  place: CopyPlace;
  plans: Plans | null;
  checked: Set<string>;
  overwrite: Set<string>;
  onToggle: (key: string) => void;
  onOverwrite: (key: string) => void;
  onTogglePlace: (keys: string[]) => void;
}) {
  const t = useT().copy;
  const keys = placeKeys(p);
  const on = keys.filter((k) => checked.has(k)).length;
  const box = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (box.current) box.current.indeterminate = on > 0 && on < keys.filter((k) => canCheck(plans?.get(k))).length;
  });
  const bySource = new Map<string, Skill[]>();
  for (const x of p.skills) {
    const label = sourceLabel(s, x.source);
    bySource.set(label, [...(bySource.get(label) ?? []), x]);
  }
  return (
    <div className="copy-place" data-place={placeOf(p.to)} data-agent={p.to.agent}>
      <div className="sticky top-0 z-1 flex min-w-0 items-center gap-1.5 border-t border-border-subtle bg-sticky px-2.5 py-1 text-xs font-semibold first:border-t-0">
        {!p.missing && (
          <input
            ref={box}
            type="checkbox"
            className="copy-place-toggle shrink-0 accent-primary"
            aria-label={t.placeToggle(heading.name)}
            checked={on > 0}
            disabled={!plans}
            onChange={() => onTogglePlace(keys)}
          />
        )}
        <span className="min-w-0 truncate">{heading.name}</span>
        {heading.detail && <span className="shrink-0 font-normal text-muted-foreground">{heading.detail}</span>}
        <span className="grow" />
        <span className="shrink-0 font-normal text-muted-foreground">
          {p.missing ? t.missingPlace : t.placeCount(copyItems(p))}
        </span>
      </div>
      {!p.missing && (
        <>
          {p.groups.map((g) => (
            <GroupRow
              key={g.id}
              s={s}
              place={p}
              group={g}
              plan={plans?.get(groupKey(p, g.id))}
              on={checked.has(groupKey(p, g.id))}
              overwrite={overwrite.has(groupKey(p, g.id))}
              onToggle={() => onToggle(groupKey(p, g.id))}
              onOverwrite={() => onOverwrite(groupKey(p, g.id))}
            />
          ))}
          {[...bySource].map(([label, skills]) => (
            <div key={label}>
              <div className="px-2.5 pt-1 pl-6 font-mono text-[11px] text-muted-foreground">{label}</div>
              {skills.map((x) => (
                <SkillRow
                  key={x.id}
                  s={s}
                  skill={x}
                  plan={plans?.get(skillKey(p, x.id))}
                  on={checked.has(skillKey(p, x.id))}
                  onToggle={() => onToggle(skillKey(p, x.id))}
                />
              ))}
            </div>
          ))}
        </>
      )}
    </div>
  );
}

function SkillRow({
  s,
  skill,
  plan,
  on,
  onToggle,
}: {
  s: Snapshot;
  skill: Skill;
  plan: Planned | undefined;
  on: boolean;
  onToggle: () => void;
}) {
  const t = useT().copy;
  const single = plan && "plan" in plan ? plan.plan : undefined;
  const reason =
    plan && "error" in plan ? describeError(plan.error) : single && !copyable(single.status) ? occupiedReason(s, skill.id, single) : undefined;
  return (
    <ListRow asChild compact clickable={!!single && !reason} selected={on} className="copy-skill pl-6" data-name={skill.name}>
      <label title={skill.description || undefined}>
        <input type="checkbox" className="shrink-0 accent-primary" checked={on} disabled={!single || !!reason} onChange={onToggle} />
        <b className="shrink-0 font-semibold whitespace-nowrap">{skill.name}</b>
        {single && copyOverwrites(single.status) && (
          <Badge tone="warn" title={t.untrackedTitle(shortPath(s, single.folder))}>
            {t.untracked}
          </Badge>
        )}
        {reason && (
          <Badge tone="bad" title={reason}>
            {t.blocked}
          </Badge>
        )}
      </label>
    </ListRow>
  );
}

function GroupRow({
  s,
  place,
  group,
  plan,
  on,
  overwrite,
  onToggle,
  onOverwrite,
}: {
  s: Snapshot;
  place: CopyPlace;
  group: Group;
  plan: Planned | undefined;
  on: boolean;
  overwrite: boolean;
  onToggle: () => void;
  onOverwrite: () => void;
}) {
  const t = useT().copy;
  const members = plan && "members" in plan ? plan.members : undefined;
  const reason = plan && "error" in plan ? describeError(plan.error) : undefined;
  const coming = members?.filter((x) => x.status !== InstallPlanStatus.InstallAlreadyInstalled) ?? [];
  const untracked = coming.filter((x) => copyOverwrites(x.status));
  const blocked = coming.filter((x) => !copyable(x.status));
  return (
    <>
      <ListRow asChild compact clickable={!!members} selected={on} className="copy-group pl-6" data-name={group.name}>
        <label>
          <input type="checkbox" className="shrink-0 accent-primary" checked={on} disabled={!members} onChange={onToggle} />
          <b className="shrink-0 font-semibold whitespace-nowrap">{t.group(group.name)}</b>
          {members && <span className="shrink-0 text-[11px] text-muted-foreground">{t.groupMembers(coming.length)}</span>}
          <span className="min-w-0 grow truncate text-[11px] text-muted-foreground">{t.groupStanding(scopeLabel(s, place.to))}</span>
          {reason && (
            <Badge tone="bad" title={reason}>
              {t.blocked}
            </Badge>
          )}
        </label>
      </ListRow>
      {on && untracked.length > 0 && (
        <ListRow asChild compact clickable selected={overwrite} className="copy-overwrite pl-10">
          <label>
            <input type="checkbox" className="shrink-0 accent-primary" checked={overwrite} onChange={onOverwrite} />
            <span className="min-w-0 text-[11.5px]">{t.groupOverwrite(untracked.map((x) => x.name))}</span>
          </label>
        </ListRow>
      )}
      {on && blocked.length > 0 && (
        <ListRow compact className="pl-10 text-[11.5px] text-muted-foreground">
          {t.groupBlocked(blocked.map((x) => x.name))}
        </ListRow>
      )}
    </>
  );
}

// runCopy 按勾选逐处安装：先组安装，再直接安装;一项装不上不影响其余的。
async function runCopy(
  s: Snapshot,
  fromName: string,
  toName: string,
  places: CopyPlace[],
  plans: Plans,
  checked: Set<string>,
  overwrite: Set<string>,
) {
  const t = messages().copy;
  const a = messages().actions;
  const rows: ResultRow[] = [];
  // installed 按"哪一处的哪个技能"记，同一个技能既随组又直接安装时只算一次;groups 是记下的组安装数。
  const installed = new Set<string>();
  let groups = 0;
  const skip = (label: string, reason: string) => rows.push({ label, outcome: a.memberSkipped, tone: "warn", reason });
  const fail = (label: string, e: unknown) =>
    rows.push({ label, outcome: a.failed, tone: "bad", reason: describeError(toLibraryError(e)) });
  await ui.run(
    async () => {
      for (const p of places) {
        const scope = p.to;
        const where = scopeLabel(s, scope);
        if (p.missing) {
          skip(where, t.missingPlace);
          continue;
        }
        for (const g of p.groups) {
          const key = groupKey(p, g.id);
          const planned = plans.get(key);
          if (planned && "error" in planned) {
            skip(t.groupFailed(g.name, where), describeError(planned.error));
            continue;
          }
          if (!checked.has(key)) continue;
          try {
            const results = (await LibraryService.InstallGroup(g.id, scope, { overwrite: overwrite.has(key) })) ?? [];
            groups++;
            for (const r of results) if (r.outcome === GroupMemberOutcome.MemberInstalled) installed.add(`${scopeKey(scope)}:${r.skill_id}`);
            const skipped = results.filter((r) => r.outcome === GroupMemberOutcome.MemberSkipped);
            // 有不是库装的同名文件夹而没勾覆盖的，是用户自己选的跳过，不再列出。
            rows.push(...memberRows(s, skipped.filter((r) => overwrite.has(key) || !untrackedSkip(plans.get(key), r.skill_id))));
          } catch (e) {
            fail(t.groupFailed(g.name, where), e);
          }
        }
        for (const x of p.skills) {
          const key = skillKey(p, x.id);
          const planned = plans.get(key);
          if (!planned) continue;
          if ("error" in planned) {
            skip(`${x.name} → ${where}`, describeError(planned.error));
            continue;
          }
          if (!("plan" in planned)) continue;
          const { plan } = planned;
          if (!copyable(plan.status)) {
            skip(`${x.name} → ${where}`, occupiedReason(s, x.id, plan));
            continue;
          }
          if (!checked.has(key)) continue;
          try {
            await LibraryService.Install(x.id, scope, { overwrite: copyOverwrites(plan.status) });
            installed.add(`${scopeKey(scope)}:${x.id}`);
          } catch (e) {
            fail(`${x.name} → ${where}`, e);
          }
        }
      }
    },
    { failed: () => rows.some((r) => r.tone === "bad") },
  );
  if (rows.length) await showResults(t.resultsTitle(fromName, toName), rows, t.summary(installed.size, rows.length));
  else ui.toast(t.done(fromName, toName, installed.size, groups));
}

const untrackedSkip = (planned: Planned | undefined, id: number) =>
  !!planned && "members" in planned && planned.members.some((x) => x.skill_id === id && copyOverwrites(x.status));

// canCheck 是这一项能不能勾：组安装读出计划就能勾，直接安装还要装得了(没被占用)。
const canCheck = (planned: Planned | undefined) =>
  !!planned && ("members" in planned || ("plan" in planned && copyable(planned.plan.status)));
