// 技能详情抽屉(原型变体 A 的右侧抽屉):元数据与组成员、各作用域的安装、文件、版本历史，以及更新 / 重新安装 / 移除。
import { useEffect, useState } from "react";
import { LibraryService } from "@bindings/cmd/desktop";
import { Browser } from "@wailsio/runtime";
import {
  FileMode,
  type AgentCode,
  SkillScopeStatus,
  SourceType,
  UpdateCheckStatus,
  type FileMetadata,
  type Skill,
  type SkillVersion,
  type VersionDiff,
  type VersionID,
} from "@bindings/internal/library";
import {
  addSkillToGroup,
  createGroup,
  installSkill,
  previewCollection,
  reinstall,
  removeSkill,
  removeSkillFromGroup,
  uninstallSkill,
} from "@/lib/actions";
import { decodeContent, formatDate, formatSize, renderMarkdown } from "@/lib/content";
import { messages, useT } from "@/lib/i18n";
import type { Snapshot } from "@/lib/library";
import {
  agentName,
  agentShown,
  agentsUsedIn,
  collectionSkills,
  deletedInSource,
  hasUpdate,
  installedWhere,
  isLocal,
  noSource,
  needsReinstall,
  pending,
  projectName,
  renamedTo,
  revisionSummary,
  revisionText,
  sameNameInLibrary,
  scopeKey,
  scopeLabel,
  scopeOf,
  shortPath,
  skillScopeIn,
  skillScopeStatus,
  skillScopesOf,
  sourceKindLabel,
  sourceLabel,
  updateAvailable,
  type Place,
} from "@/lib/model";
import { useUI } from "@/lib/ui";
import { keepDeleted, notInterested, openHistory, openUpdate, previewNewSkills, rereadSkill, updateFromScope } from "@/lib/update";
import { cn } from "@/lib/utils";
import { Badge, type Tone } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { Hint } from "@/components/ui/field";
import { NativeSelect } from "@/components/ui/native-select";
import { Notice } from "@/components/ui/notice";
import { Sheet, SheetClose, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { AuditCard } from "./audit";
import { Icon, SourceIcon } from "./icons";
import { AgentMark, Chip } from "./parts";
import { ResizeHandle, useStoredWidth } from "./resizable";

function checkStatus(status: UpdateCheckStatus): { label: string; tone: Tone } {
  const t = messages().drawer;
  const known: Record<UpdateCheckStatus, { label: string; tone: Tone }> = {
    [UpdateCheckStatus.UpdateAvailable]: { label: t.checkAvailable, tone: "warn" },
    [UpdateCheckStatus.UpdateCurrent]: { label: t.checkCurrent, tone: "ok" },
    [UpdateCheckStatus.UpdateDeleted]: { label: t.checkDeleted, tone: "neutral" },
    [UpdateCheckStatus.UpdateFailed]: { label: t.checkFailed, tone: "bad" },
    [UpdateCheckStatus.$zero]: { label: t.checkCurrent, tone: "ok" },
  };
  return known[status];
}

// collectionRelation 说明技能和集合里其他技能的关系：是否在同一个来源版本上，以及其他技能有没有要处理的更新。
function collectionRelation(skill: Skill, collection: Skill[]): string {
  const others = collection.filter((x) => x.id !== skill.id);
  const todo = others.filter(pending).length;
  const summary = revisionSummary(collection);
  const t = messages().drawer;
  return [
    !summary || summary.kind === "same" ? t.sameRevisionAsOthers(others.length) : revisionText(summary),
    todo ? t.changedCount(todo) : "",
  ]
    .filter(Boolean)
    .join(" · ");
}

// sourceRef 是详情里的来源版本:Git 类来源写分支与 commit,来源版本未知(入库时和来源里的不一样)时如实写出。
function sourceRef(skill: Skill): string {
  const source = skill.source;
  switch (source.type) {
    case SourceType.SourceGitHub:
    case SourceType.SourceGit:
      return [source.ref || messages().drawer.defaultBranch, skill.revision ? skill.revision.slice(0, 12) : messages().model.revisionUnknown].join(" · ");
    case SourceType.SourceURL:
      try {
        return new URL(source.label).host;
      } catch {
        return source.label || "—";
      }
    case SourceType.SourceWellKnown:
      return source.label.split("/")[0];
    default:
      return messages().drawer.localDirectory;
  }
}

// viewLabel 是"在来源上查看"按钮的文字：只有 GitHub、GitLab 仓库与 well-known 站点有网页。
function viewLabel(type: SourceType): string {
  const t = messages().drawer;
  const known: Partial<Record<SourceType, string>> = {
    [SourceType.SourceGitHub]: t.viewOnGitHub,
    [SourceType.SourceGit]: t.viewOnGitLab,
    [SourceType.SourceWellKnown]: t.openSite,
  };
  return known[type] ?? t.openPage;
}

// 抽屉宽度：拖左边缘调整，记在这台机器上。
const drawerWidth = { storageKey: "mechbox.drawer-width", defaultWidth: 540, min: 420, max: 960 };

export function SkillDrawer({ s, skill, onClose }: { s: Snapshot; skill: Skill; onClose: () => void }) {
  const ui = useUI();
  const t = useT();
  const d = t.drawer;
  const [files, setFiles] = useState<FileMetadata[]>([]);
  const [versions, setVersions] = useState<SkillVersion[]>([]);
  const id = skill.id;

  // 更新带来新版本时 version_id 变，确认来源没变时 revision 前移，随之重读文件与版本历史。
  // 文件按版本 ID 读：浏览途中技能被更新，读到的仍是同一版本。
  useEffect(() => {
    const calls = [LibraryService.ListVersionFiles(skill.version_id), LibraryService.ListVersions(id)] as const;
    calls[0].then((f) => setFiles(f ?? []), () => {});
    calls[1].then((v) => setVersions(v ?? []), () => {});
    return () => calls.forEach((c) => void c.cancel());
  }, [id, skill.version_id, skill.revision]);

  const groups = s.groups.filter((g) => g.members?.includes(id));
  const notIn = s.groups.filter((g) => !g.members?.includes(id));
  const installed = skillScopesOf(s, id);
  const stale = installed.filter(needsReinstall).length;
  const check = skill.last_update_check;
  const c = s.collections.find((x) => x.id === skill.collection_id);
  const collection = c ? collectionSkills(s, c) : [];
  const sameName = sameNameInLibrary(s, skill);
  const [width, setWidth] = useStoredWidth(drawerWidth);

  return (
    <Sheet open onOpenChange={(open) => !open && onClose()}>
      <SheetContent
        className="top-0 right-2.5 bottom-2.5 max-w-[calc(100%-20px)]"
        style={{ width }}
        {...(!skill.description && { "aria-describedby": undefined })}
      >
        <ResizeHandle {...drawerWidth} edge="left" width={width} onWidth={setWidth} label={d.resizeWidth} className="left-0" />
        <SheetHeader className="h-11 pr-3 pl-3.5 text-[11px] font-semibold text-muted-foreground">
          <span className="grow">{d.title}</span>
          <SheetClose asChild>
            <Button variant="ghost" size="icon" aria-label={t.common.close}>
              <Icon name="close" />
            </Button>
          </SheetClose>
        </SheetHeader>
        <div className="min-h-0 flex-1 overflow-y-auto">
          <div className="flex flex-col gap-2 px-2.5 pt-3.5 pb-4">
            <div className="flex items-center gap-2.5 px-2">
              <SheetTitle className="flex-1 text-[18px] font-bold tracking-[-0.01em]">{skill.name}</SheetTitle>
              {hasUpdate(skill) && (
                <Button variant={pending(skill) ? "primary" : "default"} onClick={() => openUpdate(ui, s, skill.collection_id, { focus: id })}>
                  <Icon name="up" />
                  {d.update}
                </Button>
              )}
            </div>
            {skill.description && (
              <SheetDescription className="-mt-1 px-2 text-[12.5px] leading-[1.45] text-foreground-secondary">
                {skill.description}
              </SheetDescription>
            )}
            {renamedTo(skill) && (
              <Notice tone="warn" className="mb-2.5 flex items-center gap-2">
                <span className="grow">
                  {d.maybeRenamed(skill.name, sourceLabel(s, skill.source), renamedTo(skill))}
                </span>
                <Button size="sm" onClick={() => openUpdate(ui, s, skill.collection_id, { focus: id })}>
                  {d.view}
                </Button>
              </Notice>
            )}
            {deletedInSource(skill) &&
              (skill.kept_deleted ? (
                <Hint className="px-2">{d.keptDeleted(sourceLabel(s, skill.source))}</Hint>
              ) : (
                <Notice className="mb-2.5 flex items-center gap-2">
                  <span className="grow">
                    {d.deletedInSource(sourceLabel(s, skill.source), skill.name)}
                  </span>
                  <Button size="sm" onClick={async () => (await removeSkill(ui, s, id)) && onClose()}>
                    {d.remove}
                  </Button>
                  <Button size="sm" onClick={() => keepDeleted(ui, s, id)}>
                    {d.keep}
                  </Button>
                </Notice>
              ))}
            <div className="flex flex-wrap gap-1.5 px-2">
              {isLocal(skill.source) && (
                <Button size="sm" onClick={() => rereadSkill(ui, s, skill)} title={d.rereadTitle}>
                  <Icon name="up" />
                  {d.reread}
                </Button>
              )}
              {installed.length > 0 && (
                <Button size="sm" onClick={() => reinstall(ui, s, { skill_id: id }, t.actions.reinstallSkill(skill.name))}>
                  <Icon name="refresh" />
                  {d.reinstallAll}
                </Button>
              )}
              <Button size="sm" onClick={() => openHistory(ui, s, { skill_id: id }, d.historyTitle(skill.name))} title={d.historyHint}>
                {d.history}
              </Button>
              {/* 只有一个技能的来源没有集合这一层，从这里找回来源里的其他技能(包括点过"不感兴趣"的)。 */}
              {collection.length === 1 && c && (
                <Button size="sm" onClick={() => previewCollection(ui, c.id)} title={d.otherSkillsHint}>
                  {d.otherSkills}
                </Button>
              )}
            </div>
            {collection.length === 1 && (c?.new_skills?.length ?? 0) > 0 && (
              <div className="skill-new flex items-center gap-2 px-2 text-[11px] text-muted-foreground">
                <span className="grow">
                  {d.newSkills(sourceLabel(s, skill.source), c!.new_skills!)}
                </span>
                <Button variant="link" onClick={() => previewNewSkills(ui, c!.id)}>
                  {d.goFetch}
                </Button>
                <Button variant="link" onClick={() => notInterested(ui, c!.id, c!.new_skills!)}>
                  {d.notInterested}
                </Button>
              </div>
            )}

            <Card>
              <CardHeader title={d.metadata} />
              <dl className="meta grid grid-cols-[72px_1fr] [&:lang(en)]:grid-cols-[96px_1fr] items-center gap-x-3.5 gap-y-2.5 [&_dt]:font-medium [&_dt]:text-muted-foreground">
                <dt>{d.collection}</dt>
                {noSource(skill.source) ? (
                  <dd className="text-muted-foreground" title={d.noSourceHint}>
                    {t.model.noSource}
                  </dd>
                ) : (
                  <dd>
                    <span className="inline-flex items-center gap-[5px]">
                      <SourceIcon type={skill.source.type} className="size-3 text-muted-foreground" />
                      {sourceLabel(s, skill.source)}
                    </span>{" "}
                    <span className="text-muted-foreground">{sourceKindLabel(skill.source.type)}</span>
                  </dd>
                )}
                {collection.length > 1 && (
                  <>
                    <dt>{d.sameSource}</dt>
                    <dd>{collectionRelation(skill, collection)}</dd>
                  </>
                )}
                {!noSource(skill.source) && (
                  <>
                    <dt>{d.revision}</dt>
                    <dd>
                      <span className="font-mono">{sourceRef(skill)}</span>
                      {skill.source.view_url && (
                        <>
                          {" "}
                          <Button variant="link" onClick={() => void Browser.OpenURL(skill.source.view_url!)}>
                            {viewLabel(skill.source.type)}
                          </Button>
                        </>
                      )}
                    </dd>
                  </>
                )}
                {sameName.length > 0 && (
                  <>
                    <dt>{d.sameName}</dt>
                    <dd className="same-name-in-library">
                      {d.sameNameList(
                        sameName.length,
                        skill.name,
                        sameName.map((x) => t.actions.twin(noSource(x.source) ? t.model.noSource : sourceLabel(s, x.source), installedWhere(s, x.id))),
                      )}
                    </dd>
                  </>
                )}
                <dt>{d.fetchedAt}</dt>
                <dd>{formatDate(skill.fetched_at)}</dd>
                <dt>{d.currentVersion}</dt>
                <dd>{d.versionCount(formatDate(versions.find((v) => v.id === skill.version_id)?.created_at), versions.length)}</dd>
                {/* 本地目录来源不检查更新，只有"重新读取";没有来源的技能只能用目录里的修改更新。 */}
                {!isLocal(skill.source) && !noSource(skill.source) && (
                  <>
                    <dt>{d.updateCheck}</dt>
                    <dd>
                      {check ? (
                        <>
                          <Badge tone={checkStatus(check.status).tone}>
                            {check.renamed_to ? d.maybeRenamedTo(check.renamed_to) : checkStatus(check.status).label}
                          </Badge>{" "}
                          {check.skipped && <span className="text-[11px] text-muted-foreground">{d.skipped}</span>}
                          {updateAvailable(skill) && skill.edited && <span className="text-[11px] text-muted-foreground">{d.replacesEdits}</span>}
                          <span className="text-[11px] text-muted-foreground">{formatDate(check.checked_at)}</span>
                        </>
                      ) : (
                        <span className="text-muted-foreground">{d.neverChecked}</span>
                      )}
                    </dd>
                  </>
                )}
                <dt>{d.groups}</dt>
                <dd className="flex flex-wrap items-center gap-1.5">
                  {groups.map((g) => (
                    <Chip key={g.id} removeTitle={d.removeFromGroup} onRemove={() => removeSkillFromGroup(ui, s, g.id, id)}>
                      {g.name}
                    </Chip>
                  ))}
                  <NativeSelect
                    className="chip-add h-6 rounded-full px-2 text-[11.5px] text-muted-foreground"
                    value=""
                    onChange={(e) => {
                      const v = e.target.value;
                      if (v === "new") void createGroup(ui, id);
                      else if (v) void addSkillToGroup(ui, s, Number(v), id);
                    }}
                  >
                    <option value="">{d.addToGroup}</option>
                    {notIn.map((g) => (
                      <option key={g.id} value={g.id}>
                        {g.name}
                      </option>
                    ))}
                    <option value="new">{d.newGroup}</option>
                  </NativeSelect>
                </dd>
              </dl>
            </Card>

            {skill.source.type === SourceType.SourceGitHub && <AuditCard skillID={id} />}

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
                      const toggle = () => (p.direct ? uninstallSkill(ui, s, id, p.scope) : installSkill(ui, s, id, p.scope));
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
                              <AgentMark code={p.scope.agent} name={agentName(s, p.scope.agent)} />
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
                                  onClick={() => reinstall(ui, s, { skill_id: id, scope: p.scope }, scopeLabel(s, p.scope))}
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
                                  onClick={() => updateFromScope(ui, s, skill, p.scope)}
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
              <InstallTo s={s} skillID={id} />
              {installed.length > 0 && (
                <Hint className="mt-2">{d.switchHint()}</Hint>
              )}
            </Card>

            <FilesCard versionID={skill.version_id} files={files} />
            <VersionsCard versions={versions} currentID={skill.version_id} />

            {/* 移除放在最底下，和常用的更新、重新安装分开。 */}
            <div className="flex justify-end px-2 pt-2">
              <Button size="sm" variant="danger" onClick={async () => (await removeSkill(ui, s, id)) && onClose()}>
                {d.removeSkill}
              </Button>
            </div>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  );
}

// InstallTo 是"安装到…":先选地方(全局 / 项目),再选 Agent;已在此处用着的 Agent 排在前面，其余只列勾选显示的。
// 这个地方能选的 Agent 都装上了时，不摆一个选不了的下拉框，直接说明。
function InstallTo({ s, skillID }: { s: Snapshot; skillID: number }) {
  const ui = useUI();
  const t = useT();
  const [place, setPlace] = useState<Place>(0);
  const used = agentsUsedIn(s, place);
  const rest = s.agents.filter((a) => !used.includes(a) && agentShown(s.agentsShown, a));
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
                    {a.name}
                  </option>
                ))}
              </optgroup>
            )}
            <optgroup label={used.length ? t.drawer.otherAgents : "Agent"}>
              {rest.map((a) => (
                <option key={a.code} value={a.code} disabled={!available(a.code)}>
                  {a.name}
                </option>
              ))}
            </optgroup>
          </NativeSelect>
          <Button size="sm" variant="primary" onClick={() => installSkill(ui, s, skillID, scopeOf(chosen, place))}>
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

// FilesCard 列出当前版本的文件;选中后按需读取内容。SKILL.md 默认展开并渲染。
function FilesCard({ versionID, files }: { versionID: VersionID; files: FileMetadata[] }) {
  const tt = useT();
  const t = tt.drawer;
  const [path, setPath] = useState("SKILL.md");
  const [content, setContent] = useState<string | null | undefined>(undefined);
  const [raw, setRaw] = useState(false);
  useEffect(() => {
    setContent(undefined);
    if (!files.some((f) => f.path === path)) return;
    const call = LibraryService.GetVersionFile(versionID, path);
    call.then((f) => setContent(decodeContent(f.content)), () => setContent(null));
    return () => void call.cancel();
  }, [versionID, path, files]);
  const markdown = path.toLowerCase().endsWith(".md") && !raw;
  return (
    <Card>
      <CardHeader title={t.files} note={t.filesNote(files.length)} />
      <div className="mb-2">
        <FileTree nodes={fileTree(files)} selected={path} onSelect={setPath} />
      </div>
      {content === undefined ? null : content === null ? (
        <Notice>{tt.common.binary}</Notice>
      ) : markdown ? (
        <>
          <div className="md" dangerouslySetInnerHTML={{ __html: renderMarkdown(content) }} />
          <Button variant="link" onClick={() => setRaw(true)}>
            {t.viewSource}
          </Button>
        </>
      ) : (
        <>
          <Code>{content}</Code>
          {path.toLowerCase().endsWith(".md") && (
            <Button variant="link" onClick={() => setRaw(false)}>
              {t.viewRendered}
            </Button>
          )}
        </>
      )}
    </Card>
  );
}

type FileNode = { name: string; path: string; file?: FileMetadata; children: FileNode[] };

// fileTree 按路径把文件排成目录树;同一层目录在前、文件在后，各自按名字排序。
function fileTree(files: FileMetadata[]): FileNode[] {
  const root: FileNode = { name: "", path: "", children: [] };
  for (const f of files) {
    const parts = f.path.split("/");
    let dir = root;
    parts.forEach((name, i) => {
      const path = parts.slice(0, i + 1).join("/");
      if (i === parts.length - 1) {
        dir.children.push({ name, path, file: f, children: [] });
        return;
      }
      let next = dir.children.find((c) => !c.file && c.name === name);
      if (!next) {
        next = { name, path, children: [] };
        dir.children.push(next);
      }
      dir = next;
    });
  }
  const sort = (nodes: FileNode[]) => {
    nodes.sort((a, b) => Number(!!a.file) - Number(!!b.file) || a.name.localeCompare(b.name));
    nodes.forEach((n) => sort(n.children));
  };
  sort(root.children);
  return root.children;
}

// FileTree 渲染一层目录树;目录默认展开，点目录行折叠 / 展开，点文件行选中。
function FileTree({ nodes, selected, onSelect }: { nodes: FileNode[]; selected: string; onSelect: (path: string) => void }) {
  const t = useT().drawer;
  return (
    <div className="flex flex-col">
      {nodes.map((n) =>
        n.file ? (
          <button
            key={n.path}
            type="button"
            title={n.path}
            className={cn(
              "file-row flex w-full items-center gap-1.5 rounded-md py-1 pr-1.5 pl-[22px] text-left hover:bg-black/[0.04]",
              n.path === selected && "bg-primary-soft text-primary hover:bg-primary-soft",
            )}
            onClick={() => onSelect(n.path)}
          >
            <Icon name="file" className="size-3.5 shrink-0 text-muted-foreground" />
            <span className="grow truncate font-mono text-[11.5px]">{n.name}</span>
            {n.file.mode === FileMode.ModeExecutable && <Badge>{t.executable}</Badge>}
            <span className="text-[11px] text-muted-foreground">{formatSize(n.file.size)}</span>
          </button>
        ) : (
          <FileTreeDir key={n.path} node={n} selected={selected} onSelect={onSelect} />
        ),
      )}
    </div>
  );
}

function FileTreeDir({ node, selected, onSelect }: { node: FileNode; selected: string; onSelect: (path: string) => void }) {
  const [open, setOpen] = useState(true);
  return (
    <div>
      <button
        type="button"
        title={node.path}
        aria-expanded={open}
        className="dir-row flex w-full items-center gap-1.5 rounded-md py-1 pr-1.5 pl-1.5 text-left hover:bg-black/[0.04]"
        onClick={() => setOpen(!open)}
      >
        <Icon name="chev" className={cn("size-2.5 shrink-0 text-muted-foreground transition-transform", open && "rotate-90")} />
        <Icon name="folder" className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="grow truncate font-mono text-[11.5px]">{node.name}</span>
      </button>
      {open && (
        <div className="ml-[11px] border-l border-border-subtle pl-1">
          <FileTree nodes={node.children} selected={selected} onSelect={onSelect} />
        </div>
      )}
    </div>
  );
}

function Code({ children }: { children: string }) {
  return (
    <pre
      data-slot="code"
      className="max-h-[360px] overflow-auto rounded-lg border border-border-subtle bg-[#f8f9fb] px-3 py-2.5 font-mono text-[11.5px] leading-[1.55] break-words whitespace-pre-wrap select-text"
    >
      {children}
    </pre>
  );
}

// VersionsCard 列出版本历史(最新在前);可查看某个版本的文件，或与当前版本比较。
// 版本按这个技能入库以来的先后编号(版本 1 是入库时的那一份),不用库里全局的版本 ID。
function VersionsCard({ versions, currentID }: { versions: SkillVersion[]; currentID: VersionID }) {
  const ui = useUI();
  const tt = useT();
  const t = tt.drawer;
  const number = new Map(versions.map((v, i) => [v.id, i + 1]));
  const list = [...versions].reverse();
  const [diff, setDiff] = useState<{ id: number; diff: VersionDiff } | null>(null);
  const [viewing, setViewing] = useState<{ id: VersionID; files: FileMetadata[] } | null>(null);
  const [viewPath, setViewPath] = useState("");
  const [viewedText, setViewedText] = useState<string | null | undefined>(undefined);

  useEffect(() => {
    setViewedText(undefined);
    if (!viewing || !viewPath) return;
    const call = LibraryService.GetVersionFile(viewing.id, viewPath);
    call.then((f) => setViewedText(decodeContent(f.content)), () => setViewedText(null));
    return () => void call.cancel();
  }, [viewing, viewPath]);

  const compare = async (v: SkillVersion) => {
    const d = await ui.run(() => LibraryService.DiffVersions(v.id, currentID), { reload: false });
    if (d) setDiff({ id: v.id, diff: d });
  };
  const view = async (v: SkillVersion) => {
    const files = await ui.run(() => LibraryService.ListVersionFiles(v.id), { reload: false });
    if (files !== undefined) {
      setViewing({ id: v.id, files: files ?? [] });
      setViewPath(files?.[0]?.path ?? "");
    }
  };

  const changes = diff
    ? [
        ...(diff.diff.added ?? []).map((p) => ["added", t.added, p] as const),
        ...(diff.diff.changed ?? []).map((p) => ["changed", t.changed, p] as const),
        ...(diff.diff.deleted ?? []).map((p) => ["deleted", t.deleted, p] as const),
      ]
    : [];
  const diffColor = { added: "text-ok-strong", changed: "text-warn-strong", deleted: "text-bad-strong" };

  return (
    <Card>
      <CardHeader title={t.versions} note={t.versionsNote(versions.length)} />
      <ol className="versions flex flex-col gap-1.5">
        {list.map((v) => (
          <li key={v.id} className="flex items-center gap-3 text-xs">
            <span className="font-medium">{t.versionN(number.get(v.id))}</span>
            <span>{formatDate(v.created_at)}</span>
            {v.revision && <span className="font-mono text-[11px] text-muted-foreground">{v.revision.slice(0, 12)}</span>}
            {v.based_on ? <span className="text-[11px] text-muted-foreground">{t.basedOn(number.get(v.based_on))}</span> : null}
            {v.maybe_edited && <span className="text-[11px] text-muted-foreground">{t.maybeEdited}</span>}
            {v.id === currentID ? (
              <Badge tone="ok">{t.current}</Badge>
            ) : (
              <Button variant="link" onClick={() => compare(v)}>
                {t.compare}
              </Button>
            )}
            <Button variant="link" onClick={() => view(v)}>
              {t.viewFiles}
            </Button>
          </li>
        ))}
      </ol>
      {diff && (
        <Notice className="mt-2">
          <div className="flex items-center gap-2">
            <b>{t.versionToCurrent(number.get(diff.id))}</b>
            <span className="grow" />
            <Button variant="link" onClick={() => setDiff(null)}>
              {t.collapse}
            </Button>
          </div>
          {changes.map(([kind, label, p]) => (
            <div key={kind + p} data-diff={kind} className={cn("font-mono text-[11px]", diffColor[kind])}>
              {label} {p}
            </div>
          ))}
          {changes.length === 0 && <div className="text-[11px]">{tt.common.sameContent}</div>}
        </Notice>
      )}
      {viewing && (
        <div className="mt-2 flex flex-col gap-1.5">
          <div className="flex items-center gap-2">
            <b className="text-[11px]">{t.versionFiles(number.get(viewing.id))}</b>
            <NativeSelect value={viewPath} onChange={(e) => setViewPath(e.target.value)}>
              {viewing.files.map((f) => (
                <option key={f.path} value={f.path}>
                  {f.path}
                </option>
              ))}
            </NativeSelect>
            <span className="grow" />
            <Button variant="link" onClick={() => setViewing(null)}>
              {t.collapse}
            </Button>
          </div>
          {viewedText === null ? <Notice>{tt.common.binary}</Notice> : viewedText !== undefined ? <Code>{viewedText}</Code> : null}
        </div>
      )}
    </Card>
  );
}
