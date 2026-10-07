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
  type SkillSummary,
  type SkillVersion,
  type Source,
  type VersionDiff,
} from "@bindings/internal/library";
import {
  addSkillToGroup,
  checkUpdates,
  createGroup,
  installSkill,
  reinstall,
  removeSkill,
  removeSkillFromGroup,
  uninstallSkill,
  updateSkill,
} from "@/lib/actions";
import { decodeContent, formatDate, formatSize, renderMarkdown } from "@/lib/content";
import type { Snapshot } from "@/lib/library";
import {
  agentName,
  agentsUsedIn,
  needsReinstall,
  projectName,
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
  [UpdateCheckStatus.UpdateAvailable]: { label: "有更新", tone: "warn" },
  [UpdateCheckStatus.UpdateCurrent]: { label: "已是最新", tone: "ok" },
  [UpdateCheckStatus.UpdateFailed]: { label: "检查失败", tone: "bad" },
  [UpdateCheckStatus.$zero]: { label: "已是最新", tone: "ok" },
};

function sourceRef(source: Source): string {
  switch (source.type) {
    case SourceType.SourceGitHub:
      return [source.ref || "默认分支", source.last_seen_ref?.slice(0, 12)].filter(Boolean).join(" · ");
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

export function SkillDrawer({ s, skill, onClose }: { s: Snapshot; skill: SkillSummary; onClose: () => void }) {
  const ui = useUI();
  const [files, setFiles] = useState<FileMetadata[]>([]);
  const [versions, setVersions] = useState<SkillVersion[]>([]);
  const id = skill.id;

  // 技能内容变化(更新、重装)时 updated_at 变，随之重读详情。
  useEffect(() => {
    const calls = [LibraryService.ListSkillFiles(id), LibraryService.ListVersions(id)] as const;
    calls[0].then((f) => setFiles(f ?? []), () => {});
    calls[1].then((v) => setVersions(v ?? []), () => {});
    return () => calls.forEach((c) => void c.cancel());
  }, [id, skill.updated_at]);

  const groups = s.groups.filter((g) => g.members?.includes(id));
  const notIn = s.groups.filter((g) => !g.members?.includes(id));
  const installed = skillScopesOf(s, id);
  const stale = installed.filter(needsReinstall).length;
  const check = skill.last_update_check;

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
              <button type="button" className="pill-btn accent" onClick={() => updateSkill(ui, s, id)}>
                <Icon name="up" />
                更新到新版本
              </button>
            )}
          </div>
          {skill.description && <p className="detail-desc">{skill.description}</p>}
          <div className="detail-actions">
            <button type="button" className="pill-btn sm" onClick={() => checkUpdates(ui, s, [id])}>
              <Icon name="refresh" />
              检查更新
            </button>
            <button type="button" className="pill-btn sm" onClick={() => updateSkill(ui, s, id)}>
              <Icon name="up" />
              重新读取来源
            </button>
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
              <dt>来源版本</dt>
              <dd>
                <span className="mono">{sourceRef(skill.source)}</span>
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
                {formatDate(skill.updated_at)} · 共 {versions.length} 个版本
              </dd>
              <dt>更新检查</dt>
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
              <dt>组</dt>
              <dd className="chips">
                {groups.map((g) => (
                  <span key={g.group.id} className="chip">
                    {g.group.name}
                    <button type="button" onClick={() => removeSkillFromGroup(ui, s, g.group.id, id)} title="移出分组">
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
                    <option key={g.group.id} value={g.group.id}>
                      {g.group.name}
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
                        <div className="mono tiny muted">{shortPath(s, p.directory)}</div>
                      </td>
                      <td>
                        {p.direct && <span className="prov prov-ind">直接</span>}
                        {(p.groups ?? []).map((g) => (
                          <span key={g} className="prov prov-grp">
                            随「{s.groups.find((x) => x.group.id === g)?.group.name}」
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

          <FilesCard skillID={id} files={files} />
          <VersionsCard versions={versions} />
        </div>
      </aside>
    </>
  );
}

// InstallTo 是"安装到…":先选地方(全局 / 项目),再选 Agent;已在此处用着的 Agent 排在前面。
function InstallTo({ s, skillID }: { s: Snapshot; skillID: number }) {
  const ui = useUI();
  const [place, setPlace] = useState<Place>(0);
  const used = agentsUsedIn(s, place);
  const rest = s.agents.filter((a) => !used.includes(a));
  const available = (code: AgentCode) => !skillScopeIn(s, skillID, scopeKey(scopeOf(code, place)));
  const first = [...used, ...rest].find((a) => available(a.code));
  const [agent, setAgent] = useState<AgentCode | "">("");
  const chosen = agent && available(agent) ? agent : (first?.code ?? "");
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
        <optgroup label={used.length ? "其他 Agent" : "全部 Agent"}>
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
function FilesCard({ skillID, files }: { skillID: number; files: FileMetadata[] }) {
  const [path, setPath] = useState("SKILL.md");
  const [content, setContent] = useState<string | null | undefined>(undefined);
  const [raw, setRaw] = useState(false);
  useEffect(() => {
    setContent(undefined);
    if (!files.some((f) => f.path === path)) return;
    const call = LibraryService.GetSkillFile(skillID, path);
    call.then((f) => setContent(decodeContent(f.content)), () => setContent(null));
    return () => void call.cancel();
  }, [skillID, path, files]);
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
function VersionsCard({ versions }: { versions: SkillVersion[] }) {
  const ui = useUI();
  const list = [...versions].reverse();
  const current = list[0];
  const [diff, setDiff] = useState<{ id: number; diff: VersionDiff } | null>(null);
  const [viewing, setViewing] = useState<SkillVersion | null>(null);
  const [viewPath, setViewPath] = useState("");

  const compare = async (v: SkillVersion) => {
    const d = await ui.run(() => LibraryService.DiffVersions(v.id, current.id), { reload: false });
    if (d) setDiff({ id: v.id, diff: d });
  };
  const view = async (v: SkillVersion) => {
    const full = await ui.run(() => LibraryService.GetVersion(v.id), { reload: false });
    if (full) {
      setViewing(full);
      setViewPath(full.files?.[0]?.path ?? "");
    }
  };
  const viewed = viewing?.files?.find((f) => f.path === viewPath);
  const viewedText = viewed ? decodeContent(viewed.content) : undefined;

  return (
    <section className="card">
      <div className="card-h">
        <span>版本历史</span>
        <span className="muted tiny">{versions.length} 个版本</span>
      </div>
      <ol className="versions">
        {list.map((v, i) => (
          <li key={v.id}>
            <span className="mono">#{v.id}</span>
            <span>{formatDate(v.created_at)}</span>
            {i === 0 ? (
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
              {(viewing.files ?? []).map((f) => (
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
          {viewedText === null ? <div className="notice">二进制文件，不显示内容。</div> : <pre className="code">{viewedText}</pre>}
        </div>
      )}
    </section>
  );
}
