// 照着另一个 Agent 装：在一个 Agent 的作用域页，照着另一个 Agent 把它在全局和各已登记项目里装的库里技能一次装上。
// 只装一次、只加不减，装哪些见 model.ts 的 copyFromAgent;默认勾选与覆盖见 consent.ts。
// - 先逐个看安装计划：被占用的不能勾并写出原因;有不是库装的同名文件夹的默认不勾，勾上才覆盖。
//   组安装默认勾上，组员里有这种文件夹的默认跳过，勾上"覆盖"才覆盖。
// - 项目不在这台机器上的那一处跳过并说明;原来那个 Agent 里改过的技能、未纳管技能，确认时说出来。
// - 一项装不上不影响其余的;全部装上时只提示装了几个，有没装上的才列出结果。
import { useEffect, useState } from "react";
import { LibraryService } from "@bindings/cmd/desktop";
import { GroupMemberOutcome, InstallPlanStatus, type AgentCode, type Group, type InstallPlan, type Skill } from "@bindings/internal/library";
import { copyable, copyByDefault, copyOverwrites } from "@/lib/consent";
import { describeError } from "@/lib/errors";
import { messages, useT } from "@/lib/i18n";
import { currentSnapshot, toLibraryError, type LibraryError, type Snapshot } from "@/lib/library";
import {
  agentName,
  agentNameIn,
  copyFromAgent,
  copySources,
  projectName,
  scopeLabel,
  scopeOf,
  shortPath,
  sourceLabel,
  uncopiedUnmanaged,
  type CopyPlace,
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

// openCopyFromAgent 打开照装的确认;没有可以照着装的 Agent 时只告诉一声。
export function openCopyFromAgent(to: AgentCode) {
  const s = currentSnapshot();
  const sources = copySources(s, to).map((a) => a.code);
  if (!sources.length) {
    ui.toast(messages().copy.nothing(agentName(s, to)));
    return;
  }
  void ui.open((close) => <CopyDialog s={s} to={to} sources={sources} close={close} />);
}

const skillKey = (place: number, id: number) => `s:${place}:${id}`;
const groupKey = (place: number, id: number) => `g:${place}:${id}`;

// Planned 是一项的安装计划：直接安装是一个技能的，组安装是各成员的;读不出时是错误。Plans 按 skillKey / groupKey 存。
type Planned = { plan: InstallPlan } | { members: InstallPlan[] } | { error: LibraryError };
type Plans = Map<string, Planned>;

function CopyDialog({ s, to, sources, close }: { s: Snapshot; to: AgentCode; sources: AgentCode[]; close: () => void }) {
  const { copy: t, common } = useT();
  const [from, setFrom] = useState(sources[0]);
  const [plans, setPlans] = useState<Plans | null>(null);
  const [checked, setChecked] = useState<Set<string>>(new Set());
  const [overwrite, setOverwrite] = useState<Set<string>>(new Set());
  const places = copyFromAgent(s, from, to);
  const unmanaged = uncopiedUnmanaged(s, from, to).length;
  const fromName = agentName(s, from);
  const toName = agentName(s, to);

  useEffect(() => {
    let live = true;
    setPlans(null);
    const calls = copyFromAgent(s, from, to)
      .filter((p) => !p.missing)
      .flatMap((p) => {
        const scope = scopeOf(to, p.place);
        return [
          ...p.skills.map((x) => {
            const call = LibraryService.PlanInstall(x.id, scope);
            return { key: skillKey(p.place, x.id), call, planned: call.then((plan): Planned => ({ plan })) };
          }),
          ...p.groups.map((g) => {
            const call = LibraryService.PlanGroupInstall(g.id, scope);
            return { key: groupKey(p.place, g.id), call, planned: call.then((v): Planned => ({ members: v.members ?? [] })) };
          }),
        ];
      });
    void Promise.all(
      calls.map(({ key, planned }) => planned.then((v): [string, Planned] => [key, v], (e): [string, Planned] => [key, { error: toLibraryError(e) }])),
    ).then((entries) => {
      if (!live) return;
      setPlans(new Map(entries));
      // 组安装整组默认勾上;直接安装按 copyByDefault。读不出计划的不能勾。
      setChecked(new Set(entries.flatMap(([key, v]) => ("members" in v || ("plan" in v && copyByDefault(v.plan.status)) ? [key] : []))));
      setOverwrite(new Set());
    });
    return () => {
      live = false;
      calls.forEach(({ call }) => call.cancel());
    };
  }, [s, from, to]);

  const toggle = (set: Set<string>, key: string) => {
    const next = new Set(set);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    return next;
  };
  const skillCount = [...checked].filter((k) => k.startsWith("s:")).length;
  const groupCount = checked.size - skillCount;
  const edited = [...new Set(places.flatMap((p) => p.edited.map((x) => x.name)))];

  const submit = () => {
    if (!plans) return;
    close();
    void runCopy(s, from, to, places, plans, checked, overwrite);
  };

  return (
    <Modal
      title={t.title(toName)}
      wide
      onClose={close}
      footer={
        <>
          <span className="grow" />
          <Button onClick={close}>{common.cancel}</Button>
          <Button className="copy-agent-confirm" variant="primary" disabled={!plans || checked.size === 0} onClick={submit} autoFocus>
            {t.confirm(skillCount, groupCount)}
          </Button>
        </>
      }
    >
      <div className="flex items-center gap-2 text-[13px]">
        <span className="shrink-0 text-muted-foreground">{t.from}</span>
        <NativeSelect className="copy-agent-from" value={from} onChange={(e) => setFrom(e.target.value as AgentCode)}>
          {sources.map((code) => (
            <option key={code} value={code}>
              {agentName(s, code)}
            </option>
          ))}
        </NativeSelect>
        <span className="min-w-0 truncate font-semibold">{t.lead(toName)}</span>
      </div>
      <Hint>{t.once(fromName, toName)}</Hint>
      <ListBox scroll className="copy-agent-places">
        {places.map((p) => (
          <PlaceRows
            key={p.place}
            s={s}
            to={to}
            place={p}
            plans={plans}
            checked={checked}
            overwrite={overwrite}
            onToggle={(key) => setChecked((set) => toggle(set, key))}
            onOverwrite={(key) => setOverwrite((set) => toggle(set, key))}
          />
        ))}
      </ListBox>
      {!plans && <Hint>{t.checking}</Hint>}
      {edited.length > 0 && (
        <Notice tone="warn" className="copy-agent-edited">
          {t.edited(fromName, toName, edited)}
        </Notice>
      )}
      {unmanaged > 0 && <Notice className="copy-agent-unmanaged">{t.unmanaged(fromName, unmanaged)}</Notice>}
    </Modal>
  );
}

// PlaceRows 是一处的各项：先是组安装，再按来源分组列出直接安装的技能。
function PlaceRows({
  s,
  to,
  place: p,
  plans,
  checked,
  overwrite,
  onToggle,
  onOverwrite,
}: {
  s: Snapshot;
  to: AgentCode;
  place: CopyPlace;
  plans: Plans | null;
  checked: Set<string>;
  overwrite: Set<string>;
  onToggle: (key: string) => void;
  onOverwrite: (key: string) => void;
}) {
  const t = useT().copy;
  const m = useT().model;
  const name = p.place ? projectName(s, p.place) : m.global;
  const bySource = new Map<string, Skill[]>();
  for (const x of p.skills) {
    const label = sourceLabel(s, x.source);
    bySource.set(label, [...(bySource.get(label) ?? []), x]);
  }
  return (
    <div className="copy-agent-place" data-place={p.place}>
      <div className="sticky top-0 z-1 flex min-w-0 items-center gap-1.5 border-t border-border-subtle bg-gray-50/95 px-2.5 py-1 text-xs font-semibold first:border-t-0">
        <span className="min-w-0 truncate">{name}</span>
        <span className="shrink-0 font-normal text-muted-foreground">{agentNameIn(s, to, p.place)}</span>
        <span className="grow" />
        <span className="shrink-0 font-normal text-muted-foreground">
          {p.missing ? t.missingPlace : t.placeCount(p.groups.length + p.skills.length)}
        </span>
      </div>
      {!p.missing && (
        <>
          {p.groups.map((g) => (
            <GroupRow
              key={g.id}
              to={to}
              group={g}
              plan={plans?.get(groupKey(p.place, g.id))}
              on={checked.has(groupKey(p.place, g.id))}
              overwrite={overwrite.has(groupKey(p.place, g.id))}
              onToggle={() => onToggle(groupKey(p.place, g.id))}
              onOverwrite={() => onOverwrite(groupKey(p.place, g.id))}
              s={s}
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
                  plan={plans?.get(skillKey(p.place, x.id))}
                  on={checked.has(skillKey(p.place, x.id))}
                  onToggle={() => onToggle(skillKey(p.place, x.id))}
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
    <ListRow asChild compact clickable={!!single && !reason} selected={on} className="copy-agent-skill pl-6" data-name={skill.name}>
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
  to,
  group,
  plan,
  on,
  overwrite,
  onToggle,
  onOverwrite,
}: {
  s: Snapshot;
  to: AgentCode;
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
      <ListRow asChild compact clickable={!!members} selected={on} className="copy-agent-group pl-6" data-name={group.name}>
        <label>
          <input type="checkbox" className="shrink-0 accent-primary" checked={on} disabled={!members} onChange={onToggle} />
          <b className="shrink-0 font-semibold whitespace-nowrap">{t.group(group.name)}</b>
          {members && <span className="shrink-0 text-[11px] text-muted-foreground">{t.groupMembers(coming.length)}</span>}
          <span className="min-w-0 grow truncate text-[11px] text-muted-foreground">{t.groupStanding(agentName(s, to))}</span>
          {reason && (
            <Badge tone="bad" title={reason}>
              {t.blocked}
            </Badge>
          )}
        </label>
      </ListRow>
      {on && untracked.length > 0 && (
        <ListRow asChild compact clickable selected={overwrite} className="copy-agent-overwrite pl-10">
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
  from: AgentCode,
  to: AgentCode,
  places: CopyPlace[],
  plans: Plans,
  checked: Set<string>,
  overwrite: Set<string>,
) {
  const t = messages().copy;
  const a = messages().actions;
  const rows: ResultRow[] = [];
  let installed = 0;
  const fail = (label: string, e: unknown) =>
    rows.push({ label, outcome: a.failed, tone: "bad", reason: describeError(toLibraryError(e)) });
  await ui.run(
    async () => {
      for (const p of places) {
        const scope = scopeOf(to, p.place);
        const where = scopeLabel(s, scope);
        if (p.missing) {
          rows.push({ label: where, outcome: a.memberSkipped, tone: "warn", reason: t.missingPlace });
          continue;
        }
        for (const g of p.groups) {
          const key = groupKey(p.place, g.id);
          if (!checked.has(key)) continue;
          try {
            const results = (await LibraryService.InstallGroup(g.id, scope, { overwrite: overwrite.has(key) })) ?? [];
            installed += results.filter((r) => r.outcome === GroupMemberOutcome.MemberInstalled).length;
            const skipped = results.filter((r) => r.outcome === GroupMemberOutcome.MemberSkipped);
            // 有不是库装的同名文件夹而没勾覆盖的，是用户自己选的跳过，不再列出。
            rows.push(...memberRows(s, skipped.filter((r) => overwrite.has(key) || !untrackedSkip(plans.get(key), r.skill_id))));
          } catch (e) {
            fail(t.groupFailed(g.name, where), e);
          }
        }
        for (const x of p.skills) {
          const key = skillKey(p.place, x.id);
          const planned = plans.get(key);
          if (!planned || !("plan" in planned)) continue;
          const { plan } = planned;
          if (!copyable(plan.status)) {
            rows.push({ label: `${x.name} → ${where}`, outcome: a.memberSkipped, tone: "warn", reason: occupiedReason(s, x.id, plan) });
            continue;
          }
          if (!checked.has(key)) continue;
          try {
            await LibraryService.Install(x.id, scope, { overwrite: copyOverwrites(plan.status) });
            installed++;
          } catch (e) {
            fail(`${x.name} → ${where}`, e);
          }
        }
      }
    },
    { failed: () => rows.some((r) => r.tone === "bad") },
  );
  const fromName = agentName(s, from);
  const toName = agentName(s, to);
  if (rows.length) await showResults(t.resultsTitle(fromName, toName), rows, t.summary(installed, rows.length));
  else ui.toast(t.done(fromName, toName, installed));
}

const untrackedSkip = (planned: Planned | undefined, id: number) =>
  !!planned && "members" in planned && planned.members.some((x) => x.skill_id === id && copyOverwrites(x.status));
