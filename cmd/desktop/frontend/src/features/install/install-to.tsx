// 选装到哪里：先选地方(全局 / 项目),再选 Agent。详情里的"安装到…"和勾选后操作栏的"安装到…"共用这两个下拉框，
// 这个地方在用的 Agent 排在前面，其余只列勾选显示的;项目里共用一个目录的几个 Agent 合成一个选项。
import { useState } from "react";
import type { AgentCode, Scope, SkillID } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import { agentNameIn, agentShown, agentsUsedIn, mergeAgentsExcept, projectName, scopeKey, scopeOf, skillScopeIn, type Place } from "@/lib/model";
import { Modal, ui } from "@/lib/ui";
import { Button } from "@/components/ui/button";
import { Field, Hint } from "@/components/ui/field";
import { NativeSelect } from "@/components/ui/native-select";
import { installSkills } from "./install";

// useInstallTarget 是选到的地方与 Agent:available 是这个 Agent 能不能选(要装的都已直接安装在那里的不能选);
// 选的 Agent 换了地方后不在选项里、或不能选时，退回第一个能选的。agent 为空表示这个地方没有能选的 Agent。
export function useInstallTarget(available: (scope: Scope) => boolean, initial: { place?: Place; agent?: AgentCode } = {}) {
  const s = useSnapshot();
  const [place, setPlace] = useState<Place>(initial.place ?? 0);
  const [picked, setAgent] = useState<AgentCode | "">(initial.agent ?? "");
  const used = agentsUsedIn(s, place);
  const rest = mergeAgentsExcept(s, place, s.agents.filter((a) => agentShown(s.agentsShown, a)), used);
  const can = (code: AgentCode) => available(scopeOf(code, place));
  const options = [...used, ...rest];
  const agent: AgentCode | "" = picked && options.some((a) => a.code === picked) && can(picked) ? picked : (options.find((a) => can(a.code))?.code ?? "");
  return { place, setPlace, agent, setAgent, used, rest, can, options };
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

export function AgentSelect({ target }: { target: InstallTarget }) {
  const s = useSnapshot();
  const t = useT();
  const { place, used, rest, can } = target;
  return (
    <NativeSelect value={target.agent} onChange={(e) => target.setAgent(e.target.value as AgentCode)}>
      {used.length > 0 && (
        <optgroup label={t.drawer.usedHere}>
          {used.map((a) => (
            <option key={a.code} value={a.code} disabled={!can(a.code)}>
              {agentNameIn(s, a.code, place)}
            </option>
          ))}
        </optgroup>
      )}
      <optgroup label={used.length ? t.drawer.otherAgents : t.drawer.agent}>
        {rest.map((a) => (
          <option key={a.code} value={a.code} disabled={!can(a.code)}>
            {agentNameIn(s, a.code, place)}
          </option>
        ))}
      </optgroup>
    </NativeSelect>
  );
}

// installPicked 是勾选后操作栏的"安装到…":先选装到哪里，再把勾选的技能一起安装。initial 是默认选的地方与 Agent
// (作用域页是这个作用域，项目页是这个项目)。
export async function installPicked(ids: SkillID[], initial: { place?: Place; agent?: AgentCode } = {}) {
  const scope = await ui.open<Scope>((close) => <InstallToDialog ids={ids} initial={initial} close={close} />);
  if (scope) await installSkills(ids, scope);
}

function InstallToDialog({
  ids,
  initial,
  close,
}: {
  ids: SkillID[];
  initial: { place?: Place; agent?: AgentCode };
  close: (scope?: Scope) => void;
}) {
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
          <Button variant="primary" disabled={!target.agent} onClick={() => target.agent && close(scopeOf(target.agent, target.place))}>
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
            <AgentSelect target={target} />
          </Field>
        )}
      </div>
      {!target.agent && (
        <Hint className="mt-2">{target.options.length ? t.list.pickAllInstalled(placeName) : t.drawer.noAgents(placeName)}</Hint>
      )}
      <Hint className="mt-2">{t.list.pickInstallHint}</Hint>
    </Modal>
  );
}
