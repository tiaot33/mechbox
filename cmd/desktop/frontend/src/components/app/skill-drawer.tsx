// 技能详情抽屉(原型变体 A 的右侧抽屉):元数据与组成员、各作用域的安装、文件、版本历史，以及更新 / 重新安装 / 移除。
import { useEffect, useState } from "react";
import { LibraryService } from "@bindings/cmd/desktop";
import { Browser } from "@wailsio/runtime";
import {
  FileMode,
  type AgentCode,
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
  reinstall,
  removeSkill,
  removeSkillFromGroup,
  openUpdate,
  uninstallSkill,
} from "@/lib/actions";
import { decodeContent, formatDate, formatSize, renderMarkdown } from "@/lib/content";
import type { Snapshot } from "@/lib/library";
import {
  agentName,
  agentShown,
  agentsUsedIn,
  collectionSkills,
  deletedInSource,
  isLocal,
  needsReinstall,
  projectName,
  revisionSummary,
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
import { Icon, SourceIcon } from "./icons";
import { AgentMark } from "./parts";

const checkStatus: Record<UpdateCheckStatus, { label: string; tone: string }> = {
  [UpdateCheckStatus.UpdateAvailable]: { label: "有新版本", tone: "warn" },
  [UpdateCheckStatus.UpdateCurrent]: { label: "已是最新", tone: "ok" },
  [UpdateCheckStatus.UpdateDeleted]: { label: "来源里已删除", tone: "" },
  [UpdateCheckStatus.UpdateFailed]: { label: "检查失败", tone: "bad" },
  [UpdateCheckStatus.$zero]: { label: "已是最新", tone: "ok" },
};

// collectionRelation 说明技能和集合里其他技能的关系：是否在同一个来源版本上，以及其他技能有没有新版本。
function collectionRelation(skill: Skill, collection: Skill[]): string {
  const others = collection.filter((x) => x.id !== skill.id);
  const pending = others.filter(updateAvailable).length;
  const aligned = new Set(collection.map((x) => x.revision).filter(Boolean)).size <= 1;
  return [
    aligned ? `和其他 ${others.length} 个技能在同一个来源版本上` : revisionSummary(collection),
    pending ? `${pending} 个有新版本` : "",
  ]
    .filter(Boolean)
    .join(" · ");
}

function sourceRef(skill: Skill): string {
  const source = skill.source;
  switch (source.type) {
    case SourceType.SourceGitHub:
      return [source.ref || "默认分支", skill.revision?.slice(0, 12)].filter(Boolean).join(" · ");
    case SourceType.SourceURL:
      try {
        return new URL(source.label).host;
      } catch {
        return source.label || "—";
      }
    default:
      return "本地目录";
  }
}

export function SkillDrawer({ s, skill, onClose }: { s: Snapshot; skill: Skill; onClose: () => void }) {
  const ui = useUI();
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

  return (
    <>
      <div className="scrim" onClick={onClose} />
      <aside className="drawer">
        <div className="drawer-head">
          <span className="muted">技能详情</span>
          <span className="grow" />
          <button type="button" className="icon-btn" onClick={onClose} aria-label="关闭">
            <Icon name="close" />
          </button>
        </div>
        <div className="detail">
          <div className="detail-title">
            <h2>{skill.name}</h2>
            {updateAvailable(skill) && (
              <button type="button" className="pill-btn accent" onClick={() => openUpdate(ui, s, skill.collection_id, id)}>
                <Icon name="up" />
                更新
              </button>
            )}
          </div>
          {skill.description && <p className="detail-desc">{skill.description}</p>}
          {deletedInSource(skill) && (
            <div className="notice" style={{ marginBottom: 10 }}>
              作者已从 {sourceLabel(s, skill.source)} 删掉了「{skill.name}」(也可能是改了名)。你的副本还在，可以继续用。
            </div>
          )}
          <div className="detail-actions">
            {isLocal(skill.source) && (
              <button
                type="button"
                className="pill-btn sm"
                onClick={() => openUpdate(ui, s, skill.collection_id, id)}
                title="从目录读出当前内容成为新版本，并换到装过它的地方"
              >
                <Icon name="up" />
                重新读取
              </button>
            )}
            {installed.length > 0 && (
              <button type="button" className="pill-btn sm" onClick={() => reinstall(ui, s, { skill_id: id }, `「${skill.name}」`)}>
                <Icon name="refresh" />
                全部重新安装
              </button>
            )}
            <span className="grow" />
            <button
              type="button"
              className="pill-btn sm danger"
              onClick={async () => (await removeSkill(ui, s, id)) && onClose()}
            >
              移除技能
            </button>
          </div>

          <section className="card">
            <div className="card-h">
              <span>元数据</span>
            </div>
            <dl className="meta">
              <dt>集合</dt>
              <dd>
                <span className="src-chip">
                  <SourceIcon type={skill.source.type} />
                  {sourceLabel(s, skill.source)}
                </span>{" "}
                <span className="muted">{sourceKindLabel[skill.source.type]}</span>
              </dd>
              {collection.length > 1 && (
                <>
                  <dt>同一来源</dt>
                  <dd>{collectionRelation(skill, collection)}</dd>
                </>
              )}
              <dt>来源版本</dt>
              <dd>
                <span className="mono">{sourceRef(skill)}</span>
                {skill.source.view_url && (
                  <>
                    {" "}
                    <button type="button" className="link" onClick={() => void Browser.OpenURL(skill.source.view_url!)}>
                      在 GitHub 查看
                    </button>
                  </>
                )}
              </dd>
              <dt>入库于</dt>
              <dd>{formatDate(skill.fetched_at)}</dd>
              <dt>当前版本</dt>
              <dd>
                {formatDate(versions.find((v) => v.id === skill.version_id)?.created_at)} · 共 {versions.length} 个版本
              </dd>
              {/* 本地目录来源不检查更新，只有"重新读取"。 */}
              {!isLocal(skill.source) && (
                <>
                  <dt>检查更新</dt>
                  <dd>
                    {check ? (
                      <>
                        <span className={`status ${checkStatus[check.status].tone}`}>{checkStatus[check.status].label}</span>{" "}
                        <span className="muted tiny">{formatDate(check.checked_at)}</span>
                      </>
                    ) : (
                      <span className="muted">从未检查</span>
                    )}
                  </dd>
                </>
              )}
              <dt>组</dt>
              <dd className="chips">
                {groups.map((g) => (
                  <span key={g.id} className="chip">
                    {g.name}
                    <button type="button" onClick={() => removeSkillFromGroup(ui, s, g.id, id)} title="移出分组">
                      ×
                    </button>
                  </span>
                ))}
                <select
                  className="chip-add"
                  value=""
                  onChange={(e) => {
                    const v = e.target.value;
                    if (v === "new") void createGroup(ui, id);
                    else if (v) void addSkillToGroup(ui, s, Number(v), id);
                  }}
                >
                  <option value="">+ 加入分组</option>
                  {notIn.map((g) => (
                    <option key={g.id} value={g.id}>
                      {g.name}
                    </option>
                  ))}
                  <option value="new">新建组…</option>
                </select>
              </dd>
            </dl>
          </section>

          <section className="card">
            <div className="card-h">
              <span>安装</span>
              <span className="muted">
                {installed.length} 个作用域{stale ? ` · ${stale} 个需重新安装` : ""}
              </span>
            </div>
            <table className="scope-table">
              <thead>
                <tr>
                  <th />
                  <th>作用域</th>
                  <th>来源</th>
                  <th>状态</th>
                </tr>
              </thead>
              <tbody>
                {installed.map((p) => {
                  const st = skillScopeStatus[p.status];
                  const toggle = () => (p.direct ? uninstallSkill(ui, s, id, p.scope) : installSkill(ui, s, id, p.scope));
                  return (
                    <tr key={scopeKey(p.scope)}>
                      <td>
                        <button
                          type="button"
                          className={`switch ${p.direct ? "on" : "group-held"}`}
                          role="switch"
                          aria-checked={p.direct}
                          onClick={toggle}
                          title={p.direct ? "卸载直接安装" : "只随组安装;点击追加直接安装"}
                        >
                          <i />
                        </button>
                      </td>
                      <td>
                        <div className="scope-cell">
                          <AgentMark code={p.scope.agent} name={agentName(s, p.scope.agent)} />
                          {scopeLabel(s, p.scope)}
                        </div>
                        <div className="mono tiny muted">{shortPath(s, p.folder)}</div>
                      </td>
                      <td>
                        {p.direct && <span className="prov prov-ind">直接</span>}
                        {(p.groups ?? []).map((g) => (
                          <span key={g} className="prov prov-grp">
                            随「{s.groups.find((x) => x.id === g)?.name}」
                          </span>
                        ))}
                      </td>
                      <td>
                        <span className={`status ${st.tone}`}>{st.label}</span>
                        {needsReinstall(p) && (
                          <>
                            {" "}
                            <button
                              type="button"
                              className="link"
                              onClick={() => reinstall(ui, s, { skill_id: id, scope: p.scope }, scopeLabel(s, p.scope))}
                            >
                              重新安装
                            </button>
                          </>
                        )}
                      </td>
                    </tr>
                  );
                })}
                {installed.length === 0 && (
                  <tr>
                    <td colSpan={4} className="muted">
                      还没有安装到任何作用域
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
            <InstallTo s={s} skillID={id} />
            <p className="hint">
              开关只控制<b>直接安装</b>;随组安装的需在组上卸载。直接安装和组安装都撤掉时，文件夹会从该目录移除。
            </p>
          </section>

          <FilesCard versionID={skill.version_id} files={files} />
          <VersionsCard versions={versions} currentID={skill.version_id} />
        </div>
      </aside>
    </>
  );
}

// InstallTo 是"安装到…":先选地方(全局 / 项目),再选 Agent;已在此处用着的 Agent 排在前面，其余只列勾选显示的。
function InstallTo({ s, skillID }: { s: Snapshot; skillID: number }) {
  const ui = useUI();
  const [place, setPlace] = useState<Place>(0);
  const used = agentsUsedIn(s, place);
  const rest = s.agents.filter((a) => !used.includes(a) && agentShown(s.agentsShown, a));
  const available = (code: AgentCode) => !skillScopeIn(s, skillID, scopeKey(scopeOf(code, place)));
  const options = [...used, ...rest];
  const first = options.find((a) => available(a.code));
  const [agent, setAgent] = useState<AgentCode | "">("");
  // 换了地方后，之前选的 Agent 可能不在这里的选项里(不显示且这里没在用),退回第一个可选的。
  const chosen = agent && options.some((a) => a.code === agent) && available(agent) ? agent : (first?.code ?? "");
  return (
    <div className="install-to">
      <span className="tiny muted">安装到</span>
      <select value={place} onChange={(e) => setPlace(Number(e.target.value))}>
        <option value={0}>全局</option>
        {s.projects.map((p) => (
          <option key={p.id} value={p.id}>
            {projectName(s, p.id)}
          </option>
        ))}
      </select>
      <select value={chosen} onChange={(e) => setAgent(e.target.value as AgentCode)}>
        {used.length > 0 && (
          <optgroup label="此处已在用">
            {used.map((a) => (
              <option key={a.code} value={a.code} disabled={!available(a.code)}>
                {a.name}
              </option>
            ))}
          </optgroup>
        )}
        <optgroup label={used.length ? "其他 Agent" : "Agent"}>
          {rest.map((a) => (
            <option key={a.code} value={a.code} disabled={!available(a.code)}>
              {a.name}
            </option>
          ))}
        </optgroup>
      </select>
      <button
        type="button"
        className="pill-btn accent sm"
        disabled={!chosen}
        onClick={() => chosen && installSkill(ui, s, skillID, scopeOf(chosen, place))}
      >
        <Icon name="plus" />
        安装
      </button>
    </div>
  );
}

// FilesCard 列出当前版本的文件;选中后按需读取内容。SKILL.md 默认展开并渲染。
function FilesCard({ versionID, files }: { versionID: VersionID; files: FileMetadata[] }) {
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
    <section className="card">
      <div className="card-h">
        <span>文件</span>
        <span className="muted tiny">
          {files.length} 个 · 只读，内容来自技能库
        </span>
      </div>
      <div className="files" style={{ flexDirection: "column", gap: 0, marginBottom: 8 }}>
        {files.map((f) => (
          <button key={f.path} type="button" className={`file-row ${f.path === path ? "on" : ""}`} onClick={() => setPath(f.path)}>
            <span className="mono grow ellipsis">{f.path}</span>
            {f.mode === FileMode.ModeExecutable && <span className="badge">可执行</span>}
            <span className="tiny muted">{formatSize(f.size)}</span>
          </button>
        ))}
      </div>
      {content === undefined ? null : content === null ? (
        <div className="notice">二进制文件，不显示内容。</div>
      ) : markdown ? (
        <>
          <div className="md" dangerouslySetInnerHTML={{ __html: renderMarkdown(content) }} />
          <button type="button" className="link" onClick={() => setRaw(true)}>
            查看源码
          </button>
        </>
      ) : (
        <>
          <pre className="code">{content}</pre>
          {path.toLowerCase().endsWith(".md") && (
            <button type="button" className="link" onClick={() => setRaw(false)}>
              查看渲染
            </button>
          )}
        </>
      )}
    </section>
  );
}

// VersionsCard 列出版本历史(最新在前);可查看某个版本的文件，或与当前版本比较。
function VersionsCard({ versions, currentID }: { versions: SkillVersion[]; currentID: VersionID }) {
  const ui = useUI();
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

  return (
    <section className="card">
      <div className="card-h">
        <span>版本历史</span>
        <span className="muted tiny">{versions.length} 个版本</span>
      </div>
      <ol className="versions">
        {list.map((v) => (
          <li key={v.id}>
            <span className="mono">#{v.id}</span>
            <span>{formatDate(v.created_at)}</span>
            {v.revision && <span className="mono tiny muted">{v.revision.slice(0, 12)}</span>}
            {v.id === currentID ? (
              <span className="status ok">当前</span>
            ) : (
              <button type="button" className="link" onClick={() => compare(v)}>
                与当前比较
              </button>
            )}
            <button type="button" className="link" onClick={() => view(v)}>
              查看文件
            </button>
          </li>
        ))}
      </ol>
      {diff && (
        <div className="notice" style={{ marginTop: 8 }}>
          <div className="row">
            <b>#{diff.id} → 当前</b>
            <span className="grow" />
            <button type="button" className="link" onClick={() => setDiff(null)}>
              收起
            </button>
          </div>
          {[...(diff.diff.added ?? []).map((p) => ["added", "新增", p]), ...(diff.diff.changed ?? []).map((p) => ["changed", "修改", p]), ...(diff.diff.deleted ?? []).map((p) => ["deleted", "删除", p])].map(
            ([kind, label, p]) => (
              <div key={kind + p} className={`mono tiny diff-${kind}`}>
                {label} {p}
              </div>
            ),
          )}
          {!diff.diff.added?.length && !diff.diff.changed?.length && !diff.diff.deleted?.length && (
            <div className="tiny">文件内容相同</div>
          )}
        </div>
      )}
      {viewing && (
        <div style={{ marginTop: 8 }}>
          <div className="row">
            <b className="tiny">版本 #{viewing.id} 的文件</b>
            <select value={viewPath} onChange={(e) => setViewPath(e.target.value)}>
              {viewing.files.map((f) => (
                <option key={f.path} value={f.path}>
                  {f.path}
                </option>
              ))}
            </select>
            <span className="grow" />
            <button type="button" className="link" onClick={() => setViewing(null)}>
              收起
            </button>
          </div>
          {viewedText === null ? (
            <div className="notice">二进制文件，不显示内容。</div>
          ) : viewedText !== undefined ? (
            <pre className="code">{viewedText}</pre>
          ) : null}
        </div>
      )}
    </section>
  );
}
