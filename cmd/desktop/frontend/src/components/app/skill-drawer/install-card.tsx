// 技能详情里的安装卡片：各作用域的安装与状态，以及"安装到…"。
import { useState } from "react";
import { SkillScopeStatus, type AgentCode, type Skill } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import {
  agentNameIn,
  agentShown,
  agentsUsedIn,
  mergeAgentsExcept,
  mergeProjectPlaces,
  needsReinstall,
  placeOf,
  projectName,
  scopeKey,
  scopeLabel,
  scopeOf,
  shortPath,
  skillScopeIn,
  skillScopeStatus,
  skillScopesOf,
  type Place,
} from "@/lib/model";
import { installSkill, uninstallSkill } from "@/features/install/install";
import { reinstall } from "@/features/install/reinstall";
import { updateFromScope } from "@/features/update/update";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { Hint } from "@/components/ui/field";
import { NativeSelect } from "@/components/ui/native-select";
import { Switch } from "@/components/ui/switch";
import { Icon } from "@/components/app/icons";
import { PlaceAgentMark } from "@/components/app/parts";

// InstallCard 是各作用域的安装与状态：直接安装开关、经哪个组装上、是不是当前版本;不是当前版本的可以单条重新安装，
// 被手动改过的可以"用这里的修改更新"。下面是"安装到…"。
export function InstallCard({ skill }: { skill: Skill }) {
  const s = useSnapshot();
  const d = useT().drawer;
  const id = skill.id;
  const installed = mergeProjectPlaces(s, skillScopesOf(s, id));
  const stale = installed.filter(needsReinstall).length;
  return (
    <Card>
      <CardHeader title={d.install} note={d.installNote(installed.length, stale)} />
      {installed.length > 0 && (
        <table className="scope-table w-full border-collapse text-xs [&_td]:border-b [&_td]:border-border-subtle [&_td]:px-1.5 [&_td]:py-[7px] [&_td]:align-middle">
          <thead>
            <tr className="[&_th]:border-b [&_th]:border-border-subtle [&_th]:px-1.5 [&_th]:py-1 [&_th]:text-left [&_th]:text-[11px] [&_th]:font-medium [&_th]:text-muted-foreground">
              <th />
              <th>{d.scope}</th>
              <th>{d.source}</th>
              <th>{d.status}</th>
            </tr>
          </thead>
          <tbody>
            {installed.map((p) => {
              const st = skillScopeStatus(p.status);
              const toggle = () => (p.direct ? uninstallSkill(id, p.scope) : installSkill(id, p.scope));
              return (
                <tr key={scopeKey(p.scope)}>
                  <td>
                    <Switch
                      checked={p.direct}
                      held={!p.direct}
                      onCheckedChange={toggle}
                      title={p.direct ? d.uninstallDirect : d.addDirect}
                    />
                  </td>
                  <td>
                    <div className="flex items-center gap-1.5">
                      <PlaceAgentMark code={p.scope.agent} place={placeOf(p.scope)} />
                      {scopeLabel(s, p.scope)}
                    </div>
                    <div className="max-w-[250px] truncate font-mono text-[11px] text-muted-foreground">{shortPath(s, p.folder)}</div>
                  </td>
                  <td>
                    <span className="inline-flex flex-wrap gap-1">
                      {p.direct && <Badge tone="accent">{d.direct}</Badge>}
                      {(p.groups ?? []).map((g) => (
                        <Badge key={g} tone="group">
                          {d.viaGroup(s.groups.find((x) => x.id === g)?.name)}
                        </Badge>
                      ))}
                    </span>
                  </td>
                  <td>
                    <Badge tone={st.tone}>{st.label}</Badge>
                    {needsReinstall(p) && (
                      <>
                        {" "}
                        <Button
                          variant="link"
                          onClick={() => reinstall({ skill_id: id, scope: p.scope }, scopeLabel(s, p.scope))}
                        >
                          {d.reinstall}
                        </Button>
                      </>
                    )}
                    {p.status === SkillScopeStatus.SkillScopeModified && (
                      <>
                        {" "}
                        <Button
                          variant="link"
                          onClick={() => updateFromScope(skill, p.scope)}
                          title={d.updateFromHereHint}
                        >
                          {d.updateFromHere}
                        </Button>
                      </>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
      {installed.length === 0 && <p className="text-muted-foreground">{d.notInstalled}</p>}
      <InstallTo skillID={id} />
      {installed.length > 0 && (
        <Hint className="mt-2">{d.switchHint()}</Hint>
      )}
    </Card>
  );
}

// InstallTo 是"安装到…":先选地方(全局 / 项目),再选 Agent;已在此处用着的 Agent 排在前面，其余只列勾选显示的。
// 项目里共用一个目录的几个 Agent 合成一个选项。
// 这个地方能选的 Agent 都装上了时，不摆一个选不了的下拉框，直接说明。
function InstallTo({ skillID }: { skillID: number }) {
  const s = useSnapshot();
  const t = useT();
  const [place, setPlace] = useState<Place>(0);
  const used = agentsUsedIn(s, place);
  const rest = mergeAgentsExcept(s, place, s.agents.filter((a) => agentShown(s.agentsShown, a)), used);
  const available = (code: AgentCode) => !skillScopeIn(s, skillID, scopeKey(scopeOf(code, place)));
  const options = [...used, ...rest];
  const first = options.find((a) => available(a.code));
  const [agent, setAgent] = useState<AgentCode | "">("");
  // 换了地方后，之前选的 Agent 可能不在这里的选项里(不显示且这里没在用),退回第一个可选的。
  const chosen = agent && options.some((a) => a.code === agent) && available(agent) ? agent : (first?.code ?? "");
  const placeName = place ? projectName(s, place) : t.model.global;
  return (
    <div className="install-to mt-2.5 flex items-center gap-1.5">
      <span className="text-[11px] text-muted-foreground">{t.drawer.installTo}</span>
      <NativeSelect value={place} onChange={(e) => setPlace(Number(e.target.value))}>
        <option value={0}>{t.model.global}</option>
        {s.projects.map((p) => (
          <option key={p.id} value={p.id}>
            {projectName(s, p.id)}
          </option>
        ))}
      </NativeSelect>
      {chosen ? (
        <>
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
          <Button size="sm" variant="primary" onClick={() => installSkill(skillID, scopeOf(chosen, place))}>
            <Icon name="plus" />
            {t.drawer.install}
          </Button>
        </>
      ) : (
        <span className="text-[11px] text-muted-foreground">
          {options.length ? t.drawer.allInstalled(placeName) : t.drawer.noAgents(placeName)}
        </span>
      )}
    </div>
  );
}
