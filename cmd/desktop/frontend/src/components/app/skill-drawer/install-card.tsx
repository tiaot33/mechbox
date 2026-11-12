// 技能详情里的安装卡片：各作用域的安装与状态，以及"安装到…"。
import { AppService } from "@bindings/cmd/desktop";
import { SkillScopeStatus, type Skill } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import { ui } from "@/lib/ui";
import {
  folderPresent,
  mergeProjectPlaces,
  needsReinstall,
  placeOf,
  projectName,
  scopeKey,
  scopeLabel,
  shortPath,
  skillScopeIn,
  skillScopeStatus,
  skillScopesOf,
} from "@/lib/model";
import { installSkill, installSkillTo, uninstallSkill } from "@/features/install/install";
import { AgentPicker, PlaceSelect, useInstallTarget } from "@/features/install/install-to";
import { reinstall } from "@/features/install/reinstall";
import { updateFromScope } from "@/features/update/update";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { Hint } from "@/components/ui/field";
import { Switch } from "@/components/ui/switch";
import { Icon } from "@/components/app/icons";
import { PlaceAgentMark } from "@/components/app/parts";

// InstallCard 是各作用域的安装与状态：直接安装开关、经哪个组装上、是不是当前版本;不是当前版本的可以单条重新安装，
// 被手动改过的可以"用这里的修改更新"。文件夹在的地方，路径后面可以在文件管理器中显示。下面是"安装到…"。
export function InstallCard({ skill }: { skill: Skill }) {
  const s = useSnapshot();
  const { drawer: d, common } = useT();
  const id = skill.id;
  const installed = mergeProjectPlaces(s, skillScopesOf(s, id));
  const stale = installed.filter(needsReinstall).length;
  return (
    <Card>
      <CardHeader title={d.install} note={d.installNote(installed.length, stale)} />
      {installed.length > 0 && (
        <table className="scope-table w-full table-fixed border-collapse text-xs [&_td]:border-b [&_td]:border-border-subtle [&_td]:px-1.5 [&_td]:py-[7px] [&_td]:align-middle">
          {/* 固定列宽：换一个技能时来源、状态两列不左右跳。 */}
          <colgroup>
            <col className="w-14" />
            <col />
            <col className="w-24" />
            <col className="w-32" />
          </colgroup>
          <thead>
            <tr className="[&_th]:border-b [&_th]:border-border-subtle [&_th]:px-1.5 [&_th]:py-1 [&_th]:text-left [&_th]:text-[11px] [&_th]:font-medium [&_th]:text-muted-foreground">
              <th className="whitespace-nowrap">{d.directColumn}</th>
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
                    <div className="flex min-w-0 items-center gap-0.5">
                      <span className="truncate font-mono text-[11px] text-muted-foreground" title={p.folder}>{shortPath(s, p.folder)}</span>
                      {folderPresent(p) && (
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          className="reveal-folder shrink-0"
                          title={common.reveal()}
                          aria-label={common.reveal()}
                          onClick={() => void ui.run(() => AppService.RevealInFileManager(p.folder), { reload: false })}
                        >
                          <Icon name="folder" />
                        </Button>
                      )}
                    </div>
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
      {/* 换一个技能时回到默认勾选，不带着上一个技能的勾选。 */}
      <InstallTo key={id} skillID={id} />
      {installed.length > 0 && (
        <Hint className="mt-2">{d.switchHint()}</Hint>
      )}
    </Card>
  );
}

// InstallTo 是"安装到…":先选地方(全局 / 项目),再勾选装到哪几个 Agent(见 features/install/install-to)。
// 这个地方能选的 Agent 都装上了时，不摆一个选不了的列表，直接说明。
function InstallTo({ skillID }: { skillID: number }) {
  const s = useSnapshot();
  const t = useT();
  const target = useInstallTarget((sc) => !skillScopeIn(s, skillID, scopeKey(sc)));
  const placeName = target.place ? projectName(s, target.place) : t.model.global;
  return (
    <div className="install-to mt-2.5 flex flex-wrap items-center gap-1.5">
      <span className="text-[11px] text-muted-foreground">{t.drawer.installTo}</span>
      <PlaceSelect target={target} />
      {target.selectable ? (
        <>
          <AgentPicker target={target} taken={t.drawer.hasIt} />
          <Button size="sm" variant="primary" disabled={!target.scopes.length} onClick={() => void installSkillTo(skillID, target.scopes)}>
            <Icon name="plus" />
            {t.drawer.install}
          </Button>
        </>
      ) : (
        <span className="text-[11px] text-muted-foreground">
          {target.options.length ? t.drawer.allInstalled(placeName) : t.drawer.noAgents(placeName)}
        </span>
      )}
    </div>
  );
}
