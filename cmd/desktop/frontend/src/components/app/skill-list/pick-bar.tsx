// 勾选了技能以后，列表底部的操作栏：一起安装到某处、从这里卸载(作用域页、项目页)、加入组、移出组、从库里移除。
// 按 Esc 或点"取消选择"清空勾选;换页时也清空。
import { useEffect, useState } from "react";
import type { AgentCode, GroupID, Scope, SkillID } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import {
  agentNameIn,
  agentShown,
  agentsUsedIn,
  mergeAgentsExcept,
  parseScope,
  projectAgents,
  projectName,
  scopeKey,
  scopeLabel,
  scopeOf,
  skillScopeIn,
  type PageListing,
  type Place,
} from "@/lib/model";
import { Modal, ui } from "@/lib/ui";
import { clearPicked, useSortedGroups, useView } from "@/lib/view";
import { addSkillsToGroup, createGroup, removeSkillsFromGroup } from "@/features/group/group";
import { installSkills, uninstallSkills } from "@/features/install/install";
import { removeSkills } from "@/features/skill/remove";
import { Button } from "@/components/ui/button";
import { Field, Hint } from "@/components/ui/field";
import { NativeSelect } from "@/components/ui/native-select";
import { Icon } from "@/components/app/icons";

export function PickBar({ listing }: { listing: PageListing }) {
  const s = useSnapshot();
  const picked = useView((v) => v.picked);
  const t = useT().list;
  const ids = [...picked];

  // Esc 清空勾选;弹窗、详情抽屉开着时 Esc 先关它们，输入框里的 Esc 留给输入框。
  useEffect(() => {
    if (!picked.size) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || document.querySelector('[role="dialog"]')) return;
      if (e.target instanceof HTMLInputElement && e.target.type !== "checkbox") return;
      if (e.target instanceof HTMLTextAreaElement) return;
      clearPicked();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [picked.size]);

  if (!picked.size || listing.kind === "updates") return null;

  // 从这里卸载：作用域页是这个作用域，项目页是项目里装了技能的各个 Agent。
  const here: { scopes: Scope[]; where: string } | undefined =
    listing.kind === "scope"
      ? { scopes: [parseScope(listing.scope)], where: scopeLabel(s, parseScope(listing.scope)) }
      : listing.kind === "project"
        ? { scopes: projectAgents(s, listing.project, "").map((a) => parseScope(a.scope)), where: projectName(s, listing.project) }
        : undefined;
  const canUninstall = !!here?.scopes.some((sc) => ids.some((id) => skillScopeIn(s, id, scopeKey(sc))?.direct));
  const inGroups = s.groups.some((g) => ids.some((id) => g.members?.includes(id)));

  return (
    <div className="pointer-events-none absolute inset-x-0 bottom-4 z-[2] flex justify-center px-4">
      <div className="pick-bar pointer-events-auto flex max-w-full min-w-0 flex-wrap items-center gap-1 rounded-xl border border-hairline bg-white/95 px-2 py-1.5 shadow-[0_8px_28px_rgba(17,24,39,0.16)] backdrop-blur-[12px]">
        <span className="px-1.5 text-[12px] font-semibold whitespace-nowrap">{t.pickedCount(picked.size)}</span>
        <Button size="sm" variant="ghost" onClick={() => void installPicked(ids)}>
          <Icon name="plus" />
          {t.pickInstallTo}
        </Button>
        {canUninstall && (
          <Button size="sm" variant="ghost" onClick={() => void uninstallSkills(ids, here!.scopes, here!.where)}>
            {listing.kind === "project" ? t.pickUninstallProject : t.pickUninstallHere}
          </Button>
        )}
        <Button size="sm" variant="ghost" onClick={() => void pickGroup(ids, "add")}>
          <Icon name="layers" />
          {t.pickAddToGroup}
        </Button>
        {inGroups && (
          <Button size="sm" variant="ghost" onClick={() => void pickGroup(ids, "remove")}>
            {t.pickRemoveFromGroup}
          </Button>
        )}
        <Button size="sm" variant="ghost" className="text-bad-strong" onClick={() => void removeSkills(ids)}>
          {t.pickRemove}
        </Button>
        <Button size="icon-sm" variant="ghost" className="ml-0.5" title={t.pickClear} aria-label={t.pickClear} onClick={clearPicked}>
          <Icon name="close" />
        </Button>
      </div>
    </div>
  );
}

// installPicked 先选装到哪里(地方 + Agent),再一起安装。
async function installPicked(ids: SkillID[]) {
  const scope = await ui.open<Scope>((close) => <InstallToDialog ids={ids} close={close} />);
  if (scope) await installSkills(ids, scope);
}

// InstallToDialog 和详情里的"安装到…"一样先选地方再选 Agent:这个地方在用的 Agent 排在前面，其余只列勾选显示的;
// 勾选的技能都已直接安装在那里的 Agent 选不了。
function InstallToDialog({ ids, close }: { ids: SkillID[]; close: (scope?: Scope) => void }) {
  const s = useSnapshot();
  const t = useT();
  const [place, setPlace] = useState<Place>(0);
  const [agent, setAgent] = useState<AgentCode | "">("");
  const used = agentsUsedIn(s, place);
  const rest = mergeAgentsExcept(s, place, s.agents.filter((a) => agentShown(s.agentsShown, a)), used);
  const available = (code: AgentCode) => !ids.every((id) => skillScopeIn(s, id, scopeKey(scopeOf(code, place)))?.direct);
  const options = [...used, ...rest];
  const chosen = agent && options.some((a) => a.code === agent) && available(agent) ? agent : (options.find((a) => available(a.code))?.code ?? "");
  const placeName = place ? projectName(s, place) : t.model.global;
  return (
    <Modal
      title={t.list.pickInstallTitle(ids.length)}
      onClose={() => close()}
      footer={
        <>
          <Button onClick={() => close()}>{t.common.cancel}</Button>
          <Button variant="primary" disabled={!chosen} onClick={() => chosen && close(scopeOf(chosen, place))}>
            {t.actions.install}
          </Button>
        </>
      }
    >
      <div className="install-picked flex flex-wrap items-end gap-3">
        <Field label={t.list.pickPlace}>
          <NativeSelect value={place} onChange={(e) => setPlace(Number(e.target.value))}>
            <option value={0}>{t.model.global}</option>
            {s.projects.map((p) => (
              <option key={p.id} value={p.id}>
                {projectName(s, p.id)}
              </option>
            ))}
          </NativeSelect>
        </Field>
        {options.length > 0 && (
          <Field label="Agent">
            <NativeSelect value={chosen} onChange={(e) => setAgent(e.target.value as AgentCode)}>
              {used.length > 0 && (
                <optgroup label={t.drawer.usedHere}>
                  {used.map((a) => (
                    <option key={a.code} value={a.code} disabled={!available(a.code)}>
                      {agentNameIn(s, a.code, place)}
                    </option>
                  ))}
                </optgroup>
              )}
              <optgroup label={used.length ? t.drawer.otherAgents : "Agent"}>
                {rest.map((a) => (
                  <option key={a.code} value={a.code} disabled={!available(a.code)}>
                    {agentNameIn(s, a.code, place)}
                  </option>
                ))}
              </optgroup>
            </NativeSelect>
          </Field>
        )}
      </div>
      {!chosen && <Hint className="mt-2">{options.length ? t.list.pickAllInstalled(placeName) : t.drawer.noAgents(placeName)}</Hint>}
      <Hint className="mt-2">{t.list.pickInstallHint}</Hint>
    </Modal>
  );
}

// pickGroup 选一个组，把勾选的技能加入(也可以新建一个组)或移出。
async function pickGroup(ids: SkillID[], mode: "add" | "remove") {
  const chosen = await ui.open<GroupID | "new">((close) => <GroupDialog ids={ids} mode={mode} close={close} />);
  if (chosen === undefined) return;
  if (chosen === "new") await createGroup(ids);
  else if (mode === "add") await addSkillsToGroup(chosen, ids);
  else await removeSkillsFromGroup(chosen, ids);
}

// GroupDialog 选组：加入时列出全部组(勾选的都已在里面的选不了)和"新建组…";移出时只列有勾选的技能在里面的组。
function GroupDialog({ ids, mode, close }: { ids: SkillID[]; mode: "add" | "remove"; close: (group?: GroupID | "new") => void }) {
  const s = useSnapshot();
  const t = useT();
  const has = (members: SkillID[] | null | undefined, id: SkillID) => !!members?.includes(id);
  const groups = useSortedGroups(s).filter((g) => mode === "add" || ids.some((id) => has(g.members, id)));
  const full = (members: SkillID[] | null | undefined) => ids.every((id) => has(members, id));
  const first = mode === "add" ? (groups.find((g) => !full(g.members))?.id ?? "new") : groups[0]?.id;
  const [value, setValue] = useState<GroupID | "new" | undefined>(first);
  return (
    <Modal
      title={mode === "add" ? t.list.pickAddTitle(ids.length) : t.list.pickRemoveTitle(ids.length)}
      onClose={() => close()}
      footer={
        <>
          <Button onClick={() => close()}>{t.common.cancel}</Button>
          <Button variant="primary" disabled={value === undefined} onClick={() => close(value)}>
            {mode === "add" ? (value === "new" ? t.list.pickNewGroupNext : t.list.pickAdd) : t.actions.removeFromGroup}
          </Button>
        </>
      }
    >
      <Field label={t.list.pickGroup} className="group-picked">
        <NativeSelect
          className="max-w-[260px]"
          value={value ?? ""}
          onChange={(e) => setValue(e.target.value === "new" ? "new" : Number(e.target.value))}
        >
          {groups.map((g) => (
            <option key={g.id} value={g.id} disabled={mode === "add" && full(g.members)}>
              {mode === "add" && full(g.members) ? t.list.pickAllInGroup(g.name) : g.name}
            </option>
          ))}
          {mode === "add" && <option value="new">{t.list.pickNewGroup}</option>}
        </NativeSelect>
      </Field>
    </Modal>
  );
}
