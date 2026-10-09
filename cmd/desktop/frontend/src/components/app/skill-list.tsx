// 中间：作用域摘要(选中作用域时)、组 / 集合标题条(筛选组 / 集合时)、更新列表(筛选"更新"时)与技能卡片 / 列表。
// 卡片点开详情抽屉、可拖到左栏组或右栏作用域;选中作用域时卡片上的开关控制直接安装。
import type { Collection, Group, Skill } from "@bindings/internal/library";
import {
  deleteGroup,
  installGroup,
  installSkill,
  openUpdate,
  previewCollection,
  reinstall,
  renameGroup,
  uninstallGroup,
  uninstallSkill,
} from "@/lib/actions";
import { useDnD } from "@/lib/dnd";
import type { Snapshot } from "@/lib/library";
import {
  agentName,
  collectionSkills,
  deletedInSource,
  groupStateIn,
  isLocal,
  needsReinstall,
  parseScope,
  placeOf,
  projectName,
  revisionSummary,
  scopeKey,
  scopeLabel,
  shortPath,
  skillScopeIn,
  skillScopeStatus,
  skillScopesIn,
  skillScopesOf,
  sourceKindLabel,
  sourceLabel,
  sourceShort,
  updatableCollections,
  updateAvailable,
  viaGroupCount,
  type ScopeKey,
  type SkillFilter,
} from "@/lib/model";
import { useUI } from "@/lib/ui";
import { updateWithDefaults } from "@/lib/update";
import { Icon, SourceIcon } from "./icons";
import { AgentIcon, AgentMark } from "./parts";
import type { ListView } from "./toolbar";

interface SkillListProps {
  s: Snapshot;
  skills: Skill[];
  filter: SkillFilter;
  scope: ScopeKey | null;
  scopeDirectory: string | undefined;
  view: ListView;
  selected: number | null;
  onOpen: (id: number) => void;
  onFetch: () => void;
}

export function SkillList(props: SkillListProps) {
  const { s, skills, filter, scope, view } = props;
  const group = filter.kind === "group" ? s.groups.find((g) => g.id === filter.groupID) : undefined;
  const collection = filter.kind === "collection" ? s.collections.find((c) => c.id === filter.collectionID) : undefined;
  return (
    <main className="a-center tile">
      {scope && <ScopeSummary s={s} scope={scope} directory={props.scopeDirectory} />}
      {group && <GroupHead s={s} group={group} scope={scope} />}
      {collection && <CollectionHead s={s} collection={collection} />}
      {filter.kind === "updates" && updatableCollections(s).length > 0 && <UpdatesHead s={s} />}
      {/* 网格放在单独的滚动容器里：网格高度随内容，grid-auto-rows: 1fr 才等于最高那一行，而不是平分可视高度 */}
      <div className="a-scroll">
        <div className={`a-list is-${view}`}>
          {skills.map((skill) => (
            <SkillCard
              key={skill.id}
              s={s}
              skill={skill}
              scope={scope}
              selected={props.selected === skill.id}
              hideGroup={group?.id}
              onOpen={() => props.onOpen(skill.id)}
            />
          ))}
          {skills.length === 0 &&
            (s.skills.length === 0 ? (
              <div className="list-empty">
                <b>技能库还是空的</b>
                <span>从本地目录、GitHub 或技能市场把技能入库。</span>
                <button type="button" className="pill-btn accent" onClick={props.onFetch}>
                  <Icon name="plus" />
                  入库技能
                </button>
              </div>
            ) : (
              <div className="list-empty">没有匹配的技能</div>
            ))}
        </div>
      </div>
    </main>
  );
}

function SkillCard(props: {
  s: Snapshot;
  skill: Skill;
  scope: ScopeKey | null;
  selected: boolean;
  hideGroup?: number; // 正在筛选的组：每张卡都属于它，不再重复显示
  onOpen: () => void;
}) {
  const { s, skill, scope } = props;
  const ui = useUI();
  const dnd = useDnD();
  const installed = skillScopesOf(s, skill.id);
  const globals = installed.filter((p) => placeOf(p.scope) === 0).map((p) => p.scope.agent);
  const projects = new Set(installed.filter((p) => placeOf(p.scope) !== 0).map((p) => placeOf(p.scope))).size;
  const bad = installed.some(needsReinstall);
  const here = scope ? skillScopeIn(s, skill.id, scope) : undefined;
  const groups = s.groups.filter((g) => g.members?.includes(skill.id) && g.id !== props.hideGroup);
  const via = (here?.groups ?? []).map((id) => s.groups.find((g) => g.id === id)?.name ?? "");

  const toggle = (e: React.MouseEvent) => {
    e.stopPropagation();
    if (!scope) return;
    const sc = parseScope(scope);
    void (here?.direct ? uninstallSkill(ui, s, skill.id, sc) : installSkill(ui, s, skill.id, sc));
  };

  return (
    <article
      className={`a-card ${props.selected ? "selected" : ""}`}
      onClick={props.onOpen}
      draggable
      onDragStart={(e) => dnd.start(e, { kind: "skill", id: skill.id }, skill.name)}
      onDragEnd={dnd.end}
    >
      <div className="a-card-top">
        <span className="a-card-name">{skill.name}</span>
        {updateAvailable(skill) && (
          <span className="badge warn" title="来源有新版本">
            可更新
          </span>
        )}
        {deletedInSource(skill) && (
          <span className="badge" title="作者已从来源删掉它(也可能是改了名);你的副本还在，可以继续用">
            来源里已删除
          </span>
        )}
        {/* 右上角：所属组(灰色小标签，多个时显示 +N),其后是淡色的来源。 */}
        {groups.length > 0 && (
          <span className="a-card-tag" title={`组:${groups.map((g) => g.name).join("、")}`}>
            {groups[0].name}
            {groups.length > 1 && ` +${groups.length - 1}`}
          </span>
        )}
        <span className="a-card-src" title={`${sourceKindLabel[skill.source.type]} · ${sourceLabel(s, skill.source)}`}>
          {sourceShort(s, skill.source)}
        </span>
      </div>
      <p className="a-card-desc">{skill.description}</p>
      <div className="a-card-foot">
        {scope ? (
          <>
            <button
              type="button"
              className={`switch ${here?.direct ? "on" : ""} ${here && !here.direct ? "group-held" : ""}`}
              role="switch"
              aria-checked={!!here?.direct}
              onClick={toggle}
              title={here?.direct ? "卸载直接安装" : "直接安装到这个作用域"}
            >
              <i />
            </button>
            <span className="tiny">
              {here
                ? [here.direct ? "直接安装" : "", via.length ? `随「${via.join("」「")}」安装` : ""].filter(Boolean).join(" + ")
                : "未安装"}
            </span>
            {here && needsReinstall(here) && (
              <span className={`status ${skillScopeStatus[here.status].tone}`}>{skillScopeStatus[here.status].label}</span>
            )}
          </>
        ) : (
          <>
            <span className={`dot ${installed.length ? (bad ? "warn" : "on") : ""}`} />
            <span className="tiny">{globals.length ? "全局" : "未全局安装"}</span>
            {globals.length > 0 && (
              <span className="marks" title={globals.map((code) => agentName(s, code)).join("、")}>
                {globals.slice(0, 4).map((code) => (
                  <AgentIcon key={code} code={code} name={agentName(s, code)} />
                ))}
                {globals.length > 4 && <span className="tiny muted">+{globals.length - 4}</span>}
              </span>
            )}
            <span className="grow" />
            <span className="tiny muted">{projects} 个项目</span>
          </>
        )}
      </div>
    </article>
  );
}

// ScopeSummary 是选中作用域时列表顶部的摘要:Agent 目录、健康状态、全部重新安装，以及每个组的整组开关。
function ScopeSummary({ s, scope, directory }: { s: Snapshot; scope: ScopeKey; directory: string | undefined }) {
  const ui = useUI();
  const installed = skillScopesIn(s, scope);
  const issues = installed.filter(needsReinstall).length;
  const sc = parseScope(scope);
  return (
    <div className="a-scope-sum">
      <div className="a-sum-row">
        <span className="tiny muted">Agent 目录</span>
        <span className="mono tiny ellipsis" style={{ userSelect: "text" }}>
          {directory ? shortPath(s, directory) : "…"}
        </span>
        <span className="grow" />
        <span className="stat">
          <b>{installed.length}</b> 已安装
        </span>
        <span className={`stat ${issues ? "warn" : ""}`}>
          <b>{issues}</b> 需重新安装
        </span>
        {issues > 0 && (
          <button type="button" className="pill-btn sm" onClick={() => reinstall(ui, s, { scope: sc }, scopeLabel(s, sc))}>
            <Icon name="refresh" />
            全部重新安装
          </button>
        )}
      </div>
      {s.groups.length > 0 && (
        <div className="a-sum-groups">
          <span className="tiny muted">组</span>
          {s.groups.map((g) => {
            const state = groupStateIn(s, g, scope);
            return (
              <button
                type="button"
                key={g.id}
                className={`gchip ${state}`}
                onClick={() =>
                  state === "on" ? uninstallGroup(ui, s, g.id, sc) : installGroup(ui, s, g.id, sc)
                }
                title={`「${g.name}」${state === "on" ? "已组安装，点击卸载(只撤回本组的组安装)" : "未组安装，点击组安装"}`}
              >
                <i />
                {g.name}
                <span className="tiny">
                  {viaGroupCount(s, g, scope)}/{g.members?.length ?? 0}
                </span>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

// GroupHead 是筛选某个组时列表顶部的组标题条：整组的拖动把手，附带重命名、删除与组安装到当前作用域。
function GroupHead({ s, group, scope }: { s: Snapshot; group: Group; scope: ScopeKey | null }) {
  const ui = useUI();
  const dnd = useDnD();
  const scopes = s.groupInstalls.filter((a) => a.group_id === group.id).map((a) => a.scope);
  const count = group.members?.length ?? 0;
  return (
    <div
      className="a-group-head"
      draggable
      onDragStart={(e) => dnd.start(e, { kind: "group", id: group.id }, `组「${group.name}」· ${count} 个技能`)}
      onDragEnd={dnd.end}
      title={`拖到右侧作用域 = 组安装(${count} 个技能)`}
    >
      <span className="grip">
        <i />
        <i />
        <i />
        <i />
        <i />
        <i />
      </span>
      <Icon name="layers" />
      <b>{group.name}</b>
      <span className="tiny muted">{count} 个技能</span>
      <span className="a-group-on">
        {scopes.length ? (
          <>
            组安装于
            {scopes.map((sc) => (
              <span key={scopeKey(sc)} className="chip sm">
                <AgentMark code={sc.agent} name={agentName(s, sc.agent)} />
                {sc.project_id ? projectName(s, sc.project_id) : "全局"}
                <button
                  type="button"
                  title="卸载这个作用域的组安装"
                  onClick={() => uninstallGroup(ui, s, group.id, sc)}
                >
                  ×
                </button>
              </span>
            ))}
          </>
        ) : (
          <span className="tiny muted">尚未组安装到任何作用域</span>
        )}
      </span>
      <span className="card-actions">
        {scope && !scopes.some((sc) => scopeKey(sc) === scope) && (
          <button type="button" className="pill-btn sm" onClick={() => installGroup(ui, s, group.id, parseScope(scope))}>
            组安装到 {scopeLabel(s, parseScope(scope))}
          </button>
        )}
        <button type="button" className="pill-btn sm" onClick={() => renameGroup(ui, s, group.id)}>
          重命名
        </button>
        <button type="button" className="pill-btn sm danger" onClick={() => deleteGroup(ui, s, group.id)}>
          删除组
        </button>
      </span>
    </div>
  );
}

// UpdatesHead 是筛选"更新"时的更新列表：每个有新版本的集合一行。信得过的直接"更新"(按默认勾选),
// 不放心的"查看"打开更新确认框调整勾选。集合里只有一个技能时直接显示技能名。
function UpdatesHead({ s }: { s: Snapshot }) {
  const ui = useUI();
  const collections = updatableCollections(s);
  return (
    <div className="a-scope-sum">
      {collections.map((c) => {
        const skills = collectionSkills(s, c);
        const single = skills.length === 1;
        const pending = skills.filter(updateAvailable);
        const deleted = skills.filter(deletedInSource);
        const facts = [
          single ? `来自 ${sourceLabel(s, c.source)}` : revisionSummary(skills),
          single ? "有新版本" : `${pending.map((x) => x.name).join("、")} 有新版本`,
          deleted.length ? `${deleted.map((x) => x.name).join("、")} 来源里已删除` : "",
        ].filter(Boolean);
        return (
          <div key={c.id} className="a-sum-row">
            <b className="ellipsis">{single ? skills[0].name : sourceLabel(s, c.source)}</b>
            <span className="tiny muted ellipsis grow">{facts.join(" · ")}</span>
            <button type="button" className="pill-btn sm" onClick={() => openUpdate(ui, s, c.id)}>
              查看
            </button>
            <button type="button" className="pill-btn sm accent" disabled={ui.busy} onClick={() => updateWithDefaults(ui, s, [c])}>
              <Icon name="up" />
              更新
            </button>
          </div>
        );
      })}
      <div className="a-sum-row">
        <span className="tiny muted grow">更新时，装出去的地方一起换成新版;你改过的地方不动。</span>
        {collections.length > 1 && (
          <button type="button" className="pill-btn sm accent" disabled={ui.busy} onClick={() => updateWithDefaults(ui, s, collections)}>
            <Icon name="up" />
            全部更新
          </button>
        )}
      </div>
    </div>
  );
}

// CollectionHead 是筛选某个集合时的标题条：来源、集合里的技能在哪个来源版本上、更新，以及看来源里还有哪些技能。
function CollectionHead({ s, collection }: { s: Snapshot; collection: Collection }) {
  const ui = useUI();
  const skills = collectionSkills(s, collection);
  const pending = skills.filter(updateAvailable).length;
  const local = isLocal(collection.source);
  const facts = [revisionSummary(skills), pending ? `${pending} 个有新版本` : ""].filter(Boolean);
  return (
    <div className="a-scope-sum">
      <div className="a-sum-row">
        <span className="src-chip">
          <SourceIcon type={collection.source.type} />
          {sourceLabel(s, collection.source)}
        </span>
        <span className="tiny muted ellipsis grow">{facts.join(" · ")}</span>
        <button
          type="button"
          className="pill-btn sm"
          onClick={() => previewCollection(ui, collection.id)}
          title="按入库时的来源重新读取，看看作者又发布了哪些技能"
        >
          看来源里的其他技能
        </button>
        {!local && pending > 0 && (
          <button type="button" className="pill-btn sm accent" onClick={() => openUpdate(ui, s, collection.id)}>
            <Icon name="up" />
            更新
          </button>
        )}
      </div>
    </div>
  );
}
