// 照着装：照着另一个 Agent 或另一个已登记项目，把那里装的库里技能一次装过来。
// - 照着另一个 Agent(作用域页):把它在全局和各已登记项目里装的装到这个 Agent 的同一处，按地方分开列出;
//   从项目的作用域页打开时默认只勾这个项目(见 consent.ts 的 copyPlaceByDefault)。
// - 照着另一个项目(项目页):把各 Agent 在那个项目里装的装到这个项目里的同一个 Agent,按 Agent 分开列出;全局不在范围内。
// 只装一次、只加不减，装哪些、确认框里显示什么、确认后做什么见 copy-rules.ts;默认勾选与覆盖见 consent.ts。
// - 先逐个看安装计划：被占用的不能勾并写出原因;有不是库装的同名文件夹的默认不勾，勾上才覆盖。
//   组安装默认勾上，组员里有这种文件夹的默认跳过，勾上"覆盖"才覆盖。
// - 项目不在这台机器上的那一处跳过并说明;原来那里改过的技能、未纳管技能，确认时说出来。
// - 一项装不上不影响其余的;全部装上时只提示装了几个，有没装上的才列出结果。
import { useEffect, useRef, useState } from "react";
import { LibraryService } from "@bindings/cmd/desktop";
import type { AgentCode, ProjectID } from "@bindings/internal/library";
import { messages, useT } from "@/lib/i18n";
import { currentSnapshot, toLibraryError, type Snapshot } from "@/lib/library";
import { agentName, placeOf, projectName, type Place } from "@/lib/scope";
import { scopeLabel } from "@/lib/places";
import { sourceLabel } from "@/lib/source";
import { Modal, ui } from "@/lib/ui";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { ListBox, ListRow } from "@/components/ui/list";
import { NativeSelect } from "@/components/ui/native-select";
import { Notice } from "@/components/ui/notice";
import { runEach, showResults } from "@/components/app/results";
import { toastInstalled } from "./install";
import {
  agentCopy,
  copyResults,
  copySteps,
  copyView,
  plansRead,
  plansToRead,
  projectCopy,
  readingCopy,
  toggleItem,
  toggleOverwrite,
  togglePlace,
  type CopyChoices,
  type CopyGroupItem,
  type CopyKind,
  type CopyPlaceView,
  type CopySkillItem,
  type Planned,
} from "./copy-rules";

// CopyDialogKind 是一种照装(见 copy-rules 的 CopyKind)加上确认框里的文字：装到的那一处的名字(toName)、标题与说明。
// 文案都在调用时取，跟随当前界面语言。
type CopyDialogKind = CopyKind & {
  title: () => string;
  toName: string;
  lead: () => string;
  once: (from: string) => string;
};

// openCopyFromAgent 是作用域页的"照着其他 Agent 装…",at 是从哪个作用域页打开的(全局为 0);
// 从项目的作用域页打开时，这个项目排在最前、默认只勾它。没有可以照着装的 Agent 时只告诉一声。
export function openCopyFromAgent(to: AgentCode, at: Place) {
  const s = currentSnapshot();
  const kind = agentCopy(s, to, at);
  const toName = agentName(s, to);
  if (!kind.sources.length) {
    ui.toast(messages().copy.nothing(toName));
    return;
  }
  openCopy(s, {
    ...kind,
    title: () => messages().copy.title(toName),
    toName,
    lead: () => messages().copy.lead(toName),
    once: (from) => messages().copy.once(from, toName),
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
  const kind = projectCopy(s, to);
  if (!kind.sources.length) {
    ui.toast(t.projectNothing(toName));
    return;
  }
  openCopy(s, {
    ...kind,
    title: () => messages().copy.projectTitle(toName),
    toName,
    lead: () => messages().copy.projectLead(toName),
    once: (from) => messages().copy.projectOnce(from, toName),
  });
}

function openCopy(s: Snapshot, kind: CopyDialogKind) {
  void ui.open((close) => <CopyDialog s={s} kind={kind} close={close} />);
}

// CopyDialog 用打开时的快照算要装什么，不随之后的重读变：确认框开着时重读(如自动检查更新)会重算安装计划、
// 把用户改过的勾选重置掉。换了照着谁时换一份新的勾选，读完那边的安装计划再按默认规则勾上。
function CopyDialog({ s, kind, close }: { s: Snapshot; kind: CopyDialogKind; close: () => void }) {
  const { copy: t, common } = useT();
  const [from, setFrom] = useState(kind.sources[0].key);
  const [reading, setReading] = useState(() => readingCopy(kind.places(from), kind.byDefault));
  const [choices, setChoices] = useState<CopyChoices>(reading);
  const left = kind.left(from);
  const fromName = kind.sources.find((x) => x.key === from)?.name ?? from;
  const view = copyView(s, choices);

  const pickFrom = (key: string) => {
    const next = readingCopy(kind.places(key), kind.byDefault);
    setFrom(key);
    setReading(next);
    setChoices(next);
  };

  useEffect(() => {
    let live = true;
    const calls = plansToRead(reading).map((r) => {
      if ("skill" in r) {
        const call = LibraryService.PlanInstall(r.skill.id, r.scope);
        return { call, planned: call.then((plan): Planned => ({ plan })) };
      }
      const call = LibraryService.PlanGroupInstall(r.group.id, r.scope);
      return { call, planned: call.then((v): Planned => ({ members: v.members ?? [] })) };
    });
    void Promise.all(calls.map(({ planned }) => planned.catch((e): Planned => ({ error: toLibraryError(e) })))).then((planned) => {
      if (live) setChoices(plansRead(reading, planned));
    });
    return () => {
      live = false;
      calls.forEach(({ call }) => call.cancel());
    };
  }, [reading]);

  const submit = () => {
    if (view.reading) return;
    close();
    void runCopy(s, fromName, kind.toName, choices);
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
          <Button className="copy-confirm" variant="primary" disabled={!view.confirm.ready} onClick={submit} autoFocus>
            {t.confirm(view.confirm.skills, view.confirm.groups, view.confirm.overwrites)}
          </Button>
        </>
      }
    >
      <div className="flex items-center gap-2 text-body">
        <span className="shrink-0 text-muted-foreground">{t.from}</span>
        <NativeSelect className="copy-from" value={from} onChange={(e) => pickFrom(e.target.value)}>
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
        {view.places.map((p) => (
          <PlaceRows
            key={p.key}
            s={s}
            heading={kind.heading(p.place)}
            view={p}
            reading={view.reading}
            onToggle={(item) => setChoices((c) => toggleItem(c, item.ref))}
            onOverwrite={(item) => setChoices((c) => toggleOverwrite(c, item.ref))}
            onTogglePlace={() => setChoices((c) => togglePlace(c, p.place))}
          />
        ))}
      </ListBox>
      {view.reading && <Hint>{t.checking}</Hint>}
      {view.edited.length > 0 && (
        <Notice alert className="copy-edited">
          {t.edited(fromName, kind.toName, view.edited)}
        </Notice>
      )}
      {view.newer.length > 0 && (
        <Notice alert className="copy-newer">
          {t.newer(fromName, kind.toName, view.newer)}
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
  view,
  reading,
  onToggle,
  onOverwrite,
  onTogglePlace,
}: {
  s: Snapshot;
  heading: { name: string; detail?: string };
  view: CopyPlaceView;
  reading: boolean;
  onToggle: (item: CopySkillItem | CopyGroupItem) => void;
  onOverwrite: (item: CopyGroupItem) => void;
  onTogglePlace: () => void;
}) {
  const t = useT().copy;
  const p = view.place;
  const box = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (box.current) box.current.indeterminate = view.mixed;
  });
  const bySource = new Map<string, CopySkillItem[]>();
  for (const item of view.skills) {
    const label = sourceLabel(s, item.skill.source);
    bySource.set(label, [...(bySource.get(label) ?? []), item]);
  }
  return (
    <div className="copy-place" data-place={placeOf(p.to)} data-agent={p.to.agent}>
      <div className="sticky top-0 z-1 flex min-w-0 items-center gap-1.5 border-t border-row-line bg-panel px-2.5 py-1 text-body font-semibold first:border-t-0">
        {!p.missing && (
          <input
            ref={box}
            type="checkbox"
            className="copy-place-toggle shrink-0 accent-primary"
            aria-label={t.placeToggle(heading.name)}
            checked={view.on}
            disabled={reading}
            onChange={onTogglePlace}
          />
        )}
        <span className="min-w-0 truncate">{heading.name}</span>
        {heading.detail && <span className="shrink-0 font-normal text-muted-foreground">{heading.detail}</span>}
        <span className="grow" />
        <span className="shrink-0 font-normal text-muted-foreground">{p.missing ? t.missingPlace : t.placeCount(view.count)}</span>
      </div>
      {!p.missing && (
        <>
          {view.groups.map((item) => (
            <GroupRow key={item.group.id} s={s} place={view} item={item} onToggle={() => onToggle(item)} onOverwrite={() => onOverwrite(item)} />
          ))}
          {[...bySource].map(([label, items]) => (
            <div key={label}>
              <div className="px-2.5 pt-1 pl-6 font-mono text-caption text-muted-foreground">{label}</div>
              {items.map((item) => (
                <SkillRow key={item.skill.id} item={item} onToggle={() => onToggle(item)} />
              ))}
            </div>
          ))}
        </>
      )}
    </div>
  );
}

function SkillRow({ item, onToggle }: { item: CopySkillItem; onToggle: () => void }) {
  const t = useT().copy;
  const { skill } = item;
  return (
    <ListRow asChild compact clickable={item.can} selected={item.on} className="copy-skill pl-6" data-name={skill.name}>
      <label title={skill.description || undefined}>
        <input type="checkbox" className="shrink-0 accent-primary" checked={item.on} disabled={!item.can} onChange={onToggle} />
        <b className="shrink-0 font-semibold whitespace-nowrap">{skill.name}</b>
        {item.untracked !== undefined && (
          <Badge tone="warn" title={t.untrackedTitle(item.untracked)}>
            {t.untracked}
          </Badge>
        )}
        {item.reason && (
          <Badge tone="warn" title={item.reason}>
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
  item,
  onToggle,
  onOverwrite,
}: {
  s: Snapshot;
  place: CopyPlaceView;
  item: CopyGroupItem;
  onToggle: () => void;
  onOverwrite: () => void;
}) {
  const t = useT().copy;
  return (
    <>
      <ListRow asChild compact clickable={item.can} selected={item.on} className="copy-group pl-6" data-name={item.group.name}>
        <label>
          <input type="checkbox" className="shrink-0 accent-primary" checked={item.on} disabled={!item.can} onChange={onToggle} />
          <b className="shrink-0 font-semibold whitespace-nowrap">{t.group(item.group.name)}</b>
          {item.coming !== undefined && <span className="shrink-0 text-caption text-muted-foreground">{t.groupMembers(item.coming)}</span>}
          <span className="min-w-0 grow truncate text-caption text-muted-foreground">{t.groupStanding(scopeLabel(s, place.place.to))}</span>
          {item.reason && (
            <Badge tone="warn" title={item.reason}>
              {t.blocked}
            </Badge>
          )}
        </label>
      </ListRow>
      {item.on && item.untracked.length > 0 && (
        <ListRow asChild compact clickable selected={item.overwrite} className="copy-overwrite pl-10">
          <label>
            <input type="checkbox" className="shrink-0 accent-primary" checked={item.overwrite} onChange={onOverwrite} />
            <span className="min-w-0 text-caption">{t.groupOverwrite(item.untracked)}</span>
          </label>
        </ListRow>
      )}
      {item.on && item.blocked.length > 0 && (
        <ListRow compact className="pl-10 text-caption text-muted-foreground">
          {t.groupBlocked(item.blocked)}
        </ListRow>
      )}
    </>
  );
}

// runCopy 按勾选逐处安装：先组安装，再直接安装;一项装不上不影响其余的(要做的事与结果的归并见 copy-rules.ts)。
async function runCopy(s: Snapshot, fromName: string, toName: string, choices: CopyChoices) {
  const t = messages().copy;
  const steps = copySteps(s, choices);
  const results = await runEach(steps, (step) => {
    if ("skip" in step) return undefined;
    if ("group" in step) return LibraryService.InstallGroup(step.group.id, step.scope, { overwrite: step.overwrite }).then((r) => r ?? []);
    return LibraryService.Install(step.skill.id, step.scope, { overwrite: step.overwrite }).then(() => []);
  });
  if (!results) return;
  const { rows, installed, groups, placed } = copyResults(s, steps, results);
  // 装好后的提示和别处安装一样，写出装上的技能在那里找不到的被提到技能。
  if (rows.length) {
    await showResults(t.resultsTitle(fromName, toName), rows, t.summary(installed, rows.length));
    await toastInstalled(placed);
  } else {
    await toastInstalled(placed, t.done(fromName, toName, installed, groups));
  }
}
