// 技能详情里的安装卡片：各作用域的安装与状态，以及"安装到…"。
import { AppService } from "@bindings/cmd/desktop";
import { SkillScopeStatus, type Skill } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import { ui } from "@/lib/ui";
import { identicalsOf, identicalState, notTakenOver } from "@/lib/unmanaged";
import { folderPresent, editedHere, needsReinstall, needsRepair, skillScopeIn, skillScopeStatus, skillScopesOf } from "@/lib/installs";
import { eachPlace, scopeLabel } from "@/lib/places";
import { placeOf, projectName, scopeKey } from "@/lib/scope";
import { shortPath } from "@/lib/paths";
import { installSkill, installSkillTo, uninstallSkill } from "@/features/install/install";
import { AgentPicker, PlaceSelect, useInstallTarget } from "@/features/install/install-to";
import { reinstall } from "@/features/install/reinstall";
import { openUpdate, updateFromScope } from "@/features/update/update";
import { fetchUnmanaged } from "@/features/fetch/fetch-unmanaged";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { MoreMenu, type MenuEntry } from "@/components/app/menus";
import { Switch } from "@/components/ui/switch";
import { Icon } from "@/components/ui/icon";
import { PlaceAgentMark, Tipped } from "@/components/app/parts";

// InstallCard 是各作用域的安装与状态：直接安装开关、经哪个组装上、是不是当前版本;不是当前版本的可以单条重新安装，
// 被手动改过的可以"用这里的修改更新";改成了作者新版的(在 app 外更新过)说清楚，给"更新"。能做的事收在状态后面的「⋯」里，行高固定。
// 别处和它一模一样、不是 app 装的文件夹(就是这个技能)也列出来，可以"入库"。文件夹在的地方，路径后面可以在文件管理器中显示。下面是"安装到…"。开关只控制直接安装的说明在卡片标题的提示里。
export function InstallCard({ skill }: { skill: Skill }) {
  const s = useSnapshot();
  const { drawer: d, common } = useT();
  const id = skill.id;
  const installed = eachPlace(s, skillScopesOf(s, id));
  const stale = installed.filter(needsRepair).length;
  const copies = identicalsOf(s, id);
  return (
    <Card>
      <CardHeader title={<Tipped tip={installed.length > 0 && d.switchHint()}>{d.install}</Tipped>} note={d.installNote(installed.length, stale)} />
      {installed.length + copies.length > 0 && (
        <table className="scope-table w-full table-fixed border-collapse text-body [&_td]:border-b [&_td]:border-row-line [&_td]:px-1.5 [&_td]:py-[7px] [&_td]:align-middle">
          {/* 固定列宽：换一个技能时来源、状态两列不左右跳。 */}
          <colgroup>
            <col className="w-14" />
            <col />
            <col className="w-24" />
            <col className="w-32" />
          </colgroup>
          <thead>
            <tr className="[&_th]:border-b [&_th]:border-row-line [&_th]:px-1.5 [&_th]:py-1 [&_th]:text-left [&_th]:text-caption [&_th]:font-normal [&_th]:text-muted-foreground">
              <th className="whitespace-nowrap">{d.directColumn}</th>
              <th>{d.scope}</th>
              <th>{d.source}</th>
              <th>{d.status}</th>
            </tr>
          </thead>
          <tbody>
            {installed.map((p) => {
              const st = skillScopeStatus(p.status, p.source_latest);
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
                    <div className="flex min-w-0 items-center gap-1.5">
                      <PlaceAgentMark code={p.scope.agent} place={placeOf(p.scope)} />
                      <span className="min-w-0 truncate" title={scopeLabel(s, p.scope)}>
                        {scopeLabel(s, p.scope)}
                      </span>
                    </div>
                    <div className="flex min-w-0 items-center gap-0.5">
                      <span className="truncate font-mono text-caption text-muted-foreground" title={p.folder}>{shortPath(s, p.folder)}</span>
                      {folderPresent(p) && (
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          className="reveal-folder shrink-0"
                          title={common.reveal()}
                          aria-label={common.reveal()}
                          onClick={() => void ui.read(() => AppService.RevealInFileManager(p.folder))}
                        >
                          <Icon name="folder" />
                        </Button>
                      )}
                    </div>
                  </td>
                  <td>
                    {/* 一行写完，放不下的截断，悬停看全(直接安装又随几个组装时也不把行撑高)。 */}
                    <span
                      className="flex min-w-0 gap-1 overflow-hidden whitespace-nowrap"
                      title={[p.direct ? d.direct : "", ...(p.groups ?? []).map((g) => d.viaGroup(s.groups.find((x) => x.id === g)?.name))].filter(Boolean).join("\n")}
                    >
                      {p.direct && <Badge>{d.direct}</Badge>}
                      {(p.groups ?? []).map((g) => (
                        <Badge key={g} className="min-w-0 shrink">
                          <span className="truncate">{d.viaGroup(s.groups.find((x) => x.id === g)?.name)}</span>
                        </Badge>
                      ))}
                    </span>
                  </td>
                  <td>
                    {/* 一行写完：状态，能做的事收在后面的「⋯」里，行高和其他行一样，不被按钮撑高。 */}
                    <div className="flex min-w-0 items-center gap-1">
                      <Badge tone={st.tone} className="min-w-0 shrink" title={st.label}>
                        <span className="truncate">{st.label}</span>
                      </Badge>
                      <PlaceActions
                        actions={[
                          p.status === SkillScopeStatus.SkillScopeModified &&
                            p.source_latest && { label: d.updateToLatest, title: d.updateToLatestHint, onSelect: () => void openUpdate(skill.collection_id, { focus: id }) },
                          editedHere(p) && { label: d.updateFromHere, title: d.updateFromHereHint, className: "place-update-from-here", onSelect: () => updateFromScope(skill, p.scope) },
                          needsReinstall(p) && { label: d.reinstall, className: "place-reinstall", onSelect: () => reinstall({ skill_id: id, scope: p.scope }, scopeLabel(s, p.scope)) },
                        ]}
                      />
                    </div>
                  </td>
                </tr>
              );
            })}
            {copies.map((u) => (
              <tr key={u.folder} className="identical-place">
                <td />
                <td>
                  <div className="flex min-w-0 items-center gap-1.5">
                    <PlaceAgentMark code={u.scope.agent} place={placeOf(u.scope)} />
                    <span className="min-w-0 truncate" title={scopeLabel(s, u.scope)}>
                      {scopeLabel(s, u.scope)}
                    </span>
                  </div>
                  <div className="flex min-w-0 items-center gap-0.5">
                    <span className="truncate font-mono text-caption text-muted-foreground" title={u.folder}>{shortPath(s, u.folder)}</span>
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      className="reveal-folder shrink-0"
                      title={common.reveal()}
                      aria-label={common.reveal()}
                      onClick={() => void ui.read(() => AppService.RevealInFileManager(u.folder))}
                    >
                      <Icon name="folder" />
                    </Button>
                  </div>
                </td>
                <td>
                  <span className="identical-notes block truncate text-caption text-muted-foreground" title={identicalState(skill, u, s.info.home_dir).title}>
                    {identicalState(skill, u, s.info.home_dir).text}
                  </span>
                </td>
                <td>
                  <div className="flex min-w-0 items-center gap-1">
                    {identicalState(skill, u, s.info.home_dir).stale && (
                      <Badge tone={skillScopeStatus(SkillScopeStatus.SkillScopeStale).tone}>{skillScopeStatus(SkillScopeStatus.SkillScopeStale).label}</Badge>
                    )}
                    {/* 提交在项目仓库里、指向 Agent 目录以外的默认不入库：按钮是普通的灰底，不推用户去入库。 */}
                    <Button
                      variant={notTakenOver(u) ? "pill" : "pillQuiet"}
                      size="sm"
                      className="take-over"
                      onClick={() => void fetchUnmanaged(u)}
                      title={identicalState(skill, u, s.info.home_dir).title}
                    >
                      {d.takeOver}
                    </Button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {installed.length + copies.length === 0 && <p className="text-muted-foreground">{d.notInstalled}</p>}
      {/* 换一个技能时回到默认勾选，不带着上一个技能的勾选。 */}
      <InstallTo key={id} skillID={id} />
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
      <span className="text-caption text-muted-foreground">{t.drawer.installTo}</span>
      <PlaceSelect target={target} />
      {target.selectable ? (
        <>
          <AgentPicker target={target} taken={t.drawer.hasIt} />
          {/* 这一块里最要紧的动作：强调按钮。 */}
          <Button variant="accent" disabled={!target.scopes.length} onClick={() => void installSkillTo(skillID, target.scopes)}>
            <Icon name="plus" />
            {t.drawer.install}
          </Button>
        </>
      ) : (
        <span className="text-caption text-muted-foreground">
          {target.options.length ? t.drawer.allInstalled(placeName) : t.drawer.noAgents(placeName)}
        </span>
      )}
    </div>
  );
}

// PlaceActions 是安装表里一处能做的事(用这里的修改更新、更新到作者新版、重新安装),收在一个「⋯」菜单里;没有能做的事时不画。
function PlaceActions({ actions }: { actions: (MenuEntry | false | undefined)[] }) {
  const t = useT().drawer;
  const entries = actions.filter((x): x is MenuEntry => !!x);
  if (!entries.length) return null;
  return <MoreMenu entries={entries} label={t.placeActions} className="place-actions" align="end" />;
}
