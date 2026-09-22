// 变体 A · 设计稿三栏:从技能出发。沿用参考设计的骨架,只把假分类换成真实概念。
// 左栏筛选 = 集合(按来源自动) + 组(用户自建场景);右栏绑定 = 全局 / 已登记项目 × Agent;
// 选中一个作用域后,卡片上出现该作用域的激活开关,列表顶部出现作用域摘要(健康状态 + 整组开关)。
// 拖放一律从中间拖出:技能卡片 → 右栏作用域 = 激活,→ 左栏组 = 加入;组标题条(筛选组时出现)→ 右栏作用域 = 整组激活。
const A = {
  filter: { kind: 'all' },
  scope: null,
  q: '',
  activeOnly: false,
  view: 'card',
  drawer: null,
  left: true,
  right: true,
  expanded: new Set([1]),
  moreAgents: new Set(), // 展开了"其他 Agent"的地方:0 = 全局,否则项目 ID
  scopeMenu: false, // 顶栏作用域弹层是否打开
};

VARIANTS.A = { name: '设计稿三栏', render: renderA };

function aSkills() {
  let list = S.skills;
  const f = A.filter;
  if (f.kind === 'collection') list = list.filter(s => s.source.id === f.id);
  if (f.kind === 'group') list = list.filter(s => groupById(f.id).members.includes(s.id));
  if (f.kind === 'updates') list = list.filter(s => s.update_available);
  if (A.q) list = list.filter(s => (s.name + s.description).toLowerCase().includes(A.q.toLowerCase()));
  if (A.activeOnly && A.scope) list = list.filter(s => activationIn(s.id, parseScope(A.scope)));
  return list;
}

function aTitle() {
  const f = A.filter;
  if (f.kind === 'collection') return sourceLabel(S.collections.find(c => c.source.id === f.id).source);
  if (f.kind === 'group') return groupById(f.id).name;
  if (f.kind === 'updates') return '有可用更新';
  return '所有技能';
}

function aSet(patch) { Object.assign(A, patch); render(); }
function aFilter(kind, id) { A.filter = A.filter.kind === kind && A.filter.id === id && kind !== 'all' ? { kind: 'all' } : { kind, id }; render(); }
function aScope(key) { A.scope = A.scope === key ? null : key; if (!A.scope) A.activeOnly = false; render(); }
function aToggleProject(id) { A.expanded.has(id) ? A.expanded.delete(id) : A.expanded.add(id); render(); }
function aToggleMore(place) { A.moreAgents.has(place) ? A.moreAgents.delete(place) : A.moreAgents.add(place); render(); }

function renderA() {
  const f = A.filter;
  const row = (active, onclick, icon, label, count, extra = '') =>
    `<li class="nav-item ${active ? 'active' : ''}" onclick="${onclick}">${icon}<span class="grow ellipsis">${label}</span>${extra}<span class="count">${count}</span></li>`;
  const updates = S.skills.filter(s => s.update_available).length;

  const left = `
    <aside class="a-left ${A.left ? '' : 'collapsed'}">
      <button class="side-btn" onclick="stub('安装技能')">${I.plus}<span>安装技能</span></button>
      <button class="side-btn" onclick="stub('技能市场搜索')">${I.tag}<span>技能市场</span></button>
      <ul class="nav">
        ${row(f.kind === 'all', "aFilter('all')", I.bolt, '所有技能', S.skills.length)}
        ${updates ? row(f.kind === 'updates', "aFilter('updates')", I.up, '有可用更新', updates, '') : ''}
      </ul>
      <div class="nav-sec"><span>集合</span><span class="muted tiny">按来源</span></div>
      <ul class="nav">
        ${S.collections.map(c => row(f.kind === 'collection' && f.id === c.source.id, `aFilter('collection',${c.source.id})`, SOURCE_ICON[c.source.type], esc(sourceLabel(c.source)), c.skills.length)).join('')}
      </ul>
      <div class="nav-sec"><span>组</span><button class="icon-btn sm" title="新建分组" onclick="newGroup()">${I.plus}</button></div>
      <ul class="nav">
        ${S.groups.map(g => `<li class="nav-item ${f.kind === 'group' && f.id === g.id ? 'active' : ''}" onclick="aFilter('group',${g.id})"
            data-drop-group="${g.id}" title="点击筛选;把技能卡片拖到这里 = 加入本组">
            ${I.layers}<span class="grow ellipsis">${esc(g.name)}</span><span class="count">${g.members.length}</span></li>`).join('')}
      </ul>
      <div class="grow"></div>
      <ul class="nav">
        <li class="nav-item" onclick="stub('S3 同步')">${I.cloud}<span class="grow">同步</span><span class="muted tiny">手动</span></li>
        <li class="nav-item" onclick="stub('设置')">${I.gear}<span class="grow">设置</span></li>
      </ul>
    </aside>`;

  const sc = A.scope && parseScope(A.scope);
  const list = aSkills();
  // 组标题条:筛选某个组时出现在列表顶部,是"整组"这个对象在中间的把手。
  // 拖动方向因此统一为"从中间拖出":卡片 = 单个技能,组标题条 = 整组;左栏 / 右栏只做放置目标。
  const groupHead = f.kind === 'group' ? (() => {
    const g = groupById(f.id);
    const onScopes = [...new Set(S.activations.filter(a => a.via_groups.includes(g.id)).map(a => scopeKey(a.scope)))]
      .map(parseScope).filter(s => groupStateIn(g, s) === 'on');
    return `<div class="a-group-head" draggable="true" ondragstart="aDragStart(event,'group',${g.id})" ondragend="aDragEnd()"
        title="拖到右侧作用域 = 整组激活(${g.members.length} 个技能)">
      <span class="grip"><i></i><i></i><i></i><i></i><i></i><i></i></span>
      ${I.layers}<b>${esc(g.name)}</b><span class="tiny muted">${g.members.length} 个技能</span>
      <span class="a-group-on">${onScopes.length
        ? `整组激活于 ${onScopes.map(s => `<span class="chip sm">${agentMark(s.agent)}${s.project_id ? esc(projectName(s.project_id)) : '全局'}</span>`).join('')}`
        : '<span class="tiny muted">尚未整组激活到任何作用域</span>'}</span>
      <span class="grow"></span>
      <span class="tiny a-group-drag">拖到右侧作用域 → 整组激活</span>
    </div>`;
  })() : '';

  // 作用域摘要(原变体 B 的长处并入这里):投影目录、健康状态、全部重写,以及每个组的整组开关。
  const scopeSummary = sc ? (() => {
    const acts = activationsInScope(sc);
    const issues = acts.filter(a => a.status !== 'current').length;
    return `<div class="a-scope-sum">
      <div class="a-sum-row">
        <span class="tiny muted">投影目录</span><span class="mono tiny ellipsis">${esc(scopeDir(sc))}</span><span class="grow"></span>
        <span class="stat"><b>${acts.length}</b> 已激活</span>
        <span class="stat ${issues ? 'warn' : ''}"><b>${issues}</b> 需重写</span>
        ${issues ? `<button class="pill-btn sm" onclick="aRefreshAll()">${I.refresh}全部重写</button>` : ''}
      </div>
      <div class="a-sum-groups"><span class="tiny muted">组</span>
        ${S.groups.map(g => {
          const st = groupStateIn(g, sc);
          const via = g.members.filter(id => activationIn(id, sc)?.via_groups.includes(g.id)).length;
          return `<button class="gchip ${st}" onclick="toggleGroup(${g.id},'${A.scope}')"
            title="「${esc(g.name)}」${st === 'on' ? '已整组激活,点击停用(只撤回本组来源)' : st === 'partial' ? '部分激活,点击补齐' : '未激活,点击整组激活'}">
            <i></i>${esc(g.name)}<span class="tiny">${via}/${g.members.length}</span></button>`;
        }).join('')}
      </div>
    </div>`;
  })() : '';

  const card = s => {
    const acts = activationsOf(s.id);
    const globals = acts.filter(a => !a.scope.project_id).map(a => a.scope.agent);
    const projects = new Set(acts.filter(a => a.scope.project_id).map(a => a.scope.project_id)).size;
    const bad = acts.some(a => a.status !== 'current');
    const act = sc && activationIn(s.id, sc);
    const groups = S.groups.filter(g => g.members.includes(s.id));
    return `<article class="a-card ${A.drawer === s.id ? 'selected' : ''}" onclick="aOpen(${s.id})"
      draggable="true" ondragstart="aDragStart(event,'skill',${s.id})" ondragend="aDragEnd()">
      <div class="a-card-top">
        <span class="a-card-name">${esc(s.name)}</span>
        ${s.update_available ? `<span class="badge warn" title="来源有新版本">可更新</span>` : ''}
        <span class="badge" title="${SOURCE_KIND[s.source.type]}">${esc(sourceShort(s.source))}</span>
      </div>
      <p class="a-card-desc">${esc(s.description)}</p>
      ${A.view === 'card' && groups.length ? `<div class="a-card-groups">${groups.map(g => `<span>${esc(g.name)}</span>`).join('')}</div>` : ''}
      <div class="a-card-foot">
        ${sc ? `
          ${switchHtml(!!act?.individual, `toggleIndividual(${s.id},'${A.scope}')`)}
          <span class="tiny">${act ? (act.individual ? '独立激活' : '') + (act.via_groups.length ? `${act.individual ? ' + ' : ''}经由「${act.via_groups.map(g => esc(groupById(g).name)).join('」「')}」` : '') : '未激活'}</span>
          ${act && act.status !== 'current' ? `<span class="status ${STATUS[act.status].tone}">${STATUS[act.status].label}</span>` : ''}
        ` : `
          <span class="dot ${acts.length ? (bad ? 'warn' : 'on') : ''}"></span>
          <span class="tiny">${globals.length ? '全局' : '未全局激活'}</span>
          ${globals.length ? `<span class="marks" title="${esc(globals.map(agentName).join('、'))}">${globals.slice(0, 4).map(agentMark).join('')}${globals.length > 4 ? `<span class="tiny muted">+${globals.length - 4}</span>` : ''}</span>` : ''}
          <span class="grow"></span>
          <span class="tiny muted">${projects} 个项目</span>
        `}
      </div>
    </article>`;
  };

  const center = `
    <main class="a-center tile">
      ${scopeSummary}
      ${groupHead}
      <div class="a-list ${A.view}" id="a-list" data-keep-scroll="a-list">${list.map(card).join('') || '<div class="empty">没有匹配的技能</div>'}</div>
    </main>`;

  const scopeRow = (s, dim = false) => {
    const key = scopeKey(s);
    const acts = activationsInScope(s);
    const issues = acts.filter(a => a.status !== 'current').length;
    return `<li class="nav-item sub ${dim ? 'dim' : ''} ${A.scope === key ? 'active' : ''}" onclick="aScope('${key}')" data-drop-scope="${key}">
      ${agentMark(s.agent)}<span class="grow">${esc(agentName(s.agent))}</span>
      ${issues ? `<span class="status warn" title="${issues} 条投影与当前版本不一致">${issues}</span>` : ''}
      ${acts.length ? `<span class="count">${acts.length}</span>` : ''}</li>`;
  };
  // 每个地方只展开"用到的 Agent"(有激活,或正被选中);其余折进"+ 其他 N 个 Agent",展开后淡色显示。
  const agentRows = place => {
    const selected = A.scope && parseScope(A.scope);
    const shown = S.agents.filter(a => agentsUsedIn(place).includes(a) || (selected && selected.agent === a.code && (selected.project_id || 0) === place));
    const rest = S.agents.filter(a => !shown.includes(a));
    const scope = a => place ? { agent: a.code, project_id: place } : { agent: a.code };
    const more = A.moreAgents.has(place);
    return shown.map(a => scopeRow(scope(a))).join('')
      + (rest.length ? `<li class="nav-item sub more" onclick="aToggleMore(${place})" ${more ? '' : `data-spring="more:${place}"`}>${I.plus}<span class="grow">${more ? '收起' : `其他 ${rest.length} 个 Agent`}</span></li>` : '')
      + (more ? rest.map(a => scopeRow(scope(a), true)).join('') : '');
  };
  const right = `
    <aside class="a-right ${A.right ? '' : 'collapsed'}" id="a-right">
      <div class="nav-sec"><span>${I.globe} 全局</span></div>
      <ul class="nav">${agentRows(0)}</ul>
      <div class="nav-sec"><span>${I.folder} 已登记项目</span><button class="icon-btn sm" title="登记项目" onclick="stub('登记项目')">${I.plus}</button></div>
      <ul class="nav">
        ${S.projects.map(p => {
          const open = A.expanded.has(p.id);
          const n = S.activations.filter(a => a.scope.project_id === p.id).length;
          const used = agentsUsedIn(p.id);
          return `<li class="nav-item proj ${open ? 'open' : ''}" onclick="aToggleProject(${p.id})" title="${esc(p.path)}" ${open ? '' : `data-spring="proj:${p.id}"`}>
              ${I.folder}<span class="grow ellipsis">${esc(projectName(p.id))}</span>
              ${open ? '' : `<span class="marks">${used.map(a => agentMark(a.code)).join('')}</span>`}
              <span class="count">${n}</span><span class="chev">${I.chev}</span></li>
            ${open ? agentRows(p.id) : ''}`;
        }).join('')}
      </ul>
      <p class="hint pad">把技能卡片或组标题条拖到这里即可激活;拖到折叠的项目或"其他 Agent"上停一下会自动展开。点选一个作用域,卡片上会出现它的激活开关。</p>
    </aside>`;

  const drawer = A.drawer ? `
    <div class="scrim" onclick="aSet({drawer:null})"></div>
    <aside class="drawer" data-keep-scroll="a-drawer">
      <div class="drawer-head"><span class="muted">技能详情</span><span class="grow"></span>
        <button class="icon-btn" onclick="aSet({drawer:null})">${I.close}</button></div>
      ${renderDetail(A.drawer, { scopes: true })}
    </aside>` : '';

  // 统一工具栏:左 = 侧栏开关 + 当前筛选;中 = 作用域胶囊(A 最关键的状态);右 = 搜索与视图控制 + 右栏开关。
  // 原来列表面板里的标题行和"当前作用域"横条都并进这里,列表直接从卡片开始。
  const toolbar = titlebar(
    `<button class="icon-btn ${A.left ? 'on' : ''}" onclick="aSet({left:!A.left})" title="显示/隐藏侧栏">${I.sidebarL}</button>
     <span class="tb-crumb"><b>${esc(aTitle())}</b><span class="count" id="a-count">${list.length}</span></span>`,
    aScopePill(sc),
    `<label class="search tb-search">${I.search}<input id="a-search" placeholder="搜索技能" value="${esc(A.q)}" oninput="A.q=this.value;aRerenderList()"><kbd>⌘K</kbd></label>
     ${sc ? `<label class="toggle-label" title="只看在 ${esc(scopeLabel(sc))} 已激活的技能">${switchHtml(A.activeOnly, 'aSet({activeOnly:!A.activeOnly})')}<span>仅已激活</span></label>` : ''}
     <div class="seg icon-seg">
       <button class="${A.view === 'list' ? 'on' : ''}" onclick="aSet({view:'list'})" title="列表">${I.list}</button>
       <button class="${A.view === 'card' ? 'on' : ''}" onclick="aSet({view:'card'})" title="卡片">${I.grid}</button>
     </div>
     <button class="icon-btn ${A.right ? 'on' : ''}" onclick="aSet({right:!A.right})" title="显示/隐藏作用域栏">${I.sidebarR}</button>`);

  return `${toolbar}<div class="a-body">${left}${center}${right}${drawer}</div>${A.scopeMenu ? aScopeMenu(sc) : ''}<div id="drag-ghost" class="drag-ghost"></div>`;
}

// 顶栏中间的作用域胶囊:未选时是虚线占位,选中后显示 Agent · 地方,点开是选择弹层,× 清除。
function aScopePill(sc) {
  if (!sc) return `<button class="scope-pill empty ${A.scopeMenu ? 'open' : ''}" onclick="aSet({scopeMenu:!A.scopeMenu})">${I.globe}<span>选择作用域</span>${I.down}</button>`;
  const issues = activationsInScope(sc).filter(a => a.status !== 'current').length;
  return `<div class="scope-pill ${A.scopeMenu ? 'open' : ''}" title="${esc(scopeDir(sc))}">
    <button class="scope-pill-main" onclick="aSet({scopeMenu:!A.scopeMenu})">
      ${agentMark(sc.agent)}<b>${esc(agentName(sc.agent))}</b><span class="muted">· ${sc.project_id ? esc(projectName(sc.project_id)) : '全局'}</span>
      ${issues ? `<span class="status warn">${issues}</span>` : ''}${I.down}
    </button>
    <button class="scope-pill-clear" onclick="aScope('${A.scope}')" title="取消选择作用域">${I.close}</button>
  </div>`;
}

// 作用域弹层:按地方分组,只列在用的 Agent,其余 Agent 用每处一个下拉补上。
function aScopeMenu(sc) {
  const places = [{ id: 0, name: '全局', path: '~' }, ...S.projects.map(p => ({ id: p.id, name: projectName(p.id), path: shortPath(p.path) }))];
  return `<div class="menu-scrim" onclick="aSet({scopeMenu:false})"></div>
  <div class="scope-menu">
    ${places.map(p => {
      const used = agentsUsedIn(p.id);
      const rest = S.agents.filter(a => !used.includes(a));
      const key = a => scopeKey(p.id ? { agent: a.code, project_id: p.id } : { agent: a.code });
      return `<div class="sm-place"><span>${p.id ? I.folder : I.globe}${esc(p.name)}</span><span class="mono tiny muted">${esc(p.path)}</span></div>
        ${used.map(a => { const n = S.activations.filter(x => scopeKey(x.scope) === key(a)).length;
          return `<button class="sm-item ${A.scope === key(a) ? 'on' : ''}" onclick="aPickScope('${key(a)}')">${agentMark(a.code)}<span class="grow">${esc(a.name)}</span><span class="count">${n}</span></button>`; }).join('')}
        ${rest.length ? `<select class="sm-more" onchange="this.value && aPickScope(this.value)"><option value="">+ 其他 ${rest.length} 个 Agent</option>${rest.map(a => `<option value="${key(a)}">${esc(a.name)}</option>`).join('')}</select>` : ''}`;
    }).join('')}
    ${sc ? `<div class="sm-foot"><span class="mono tiny muted">${esc(scopeDir(sc))}</span><button class="link" onclick="aScope('${A.scope}');aSet({scopeMenu:false})">清除</button></div>` : ''}
  </div>`;
}

function aPickScope(key) {
  A.scope = key;
  A.scopeMenu = false;
  // 右栏同步展开到被选中的那一行,否则折叠的项目里看不到选中态。
  const sc = parseScope(key);
  if (sc.project_id) A.expanded.add(sc.project_id);
  if (!agentsUsedIn(sc.project_id || 0).some(a => a.code === sc.agent)) A.moreAgents.add(sc.project_id || 0);
  render();
}

function aOpen(id) { A.drawer = A.drawer === id ? null : id; render(); }

// 搜索时只重绘列表,保持输入框焦点。
function aRerenderList() {
  const el = document.getElementById('a-list');
  const tmp = document.createElement('div');
  tmp.innerHTML = renderA();
  el.replaceWith(tmp.querySelector('#a-list'));
  document.getElementById('a-count').replaceWith(tmp.querySelector('#a-count'));
}

async function aRefreshAll() {
  const sc = parseScope(A.scope);
  for (const a of activationsInScope(sc).filter(a => a.status !== 'current')) await api('Refresh', a.skill_id, sc);
  await reload();
  toast('已把该作用域的投影全部重写为当前版本');
}

// ---------- 拖放 ----------
// 技能卡片 → 右栏作用域 = 独立激活;技能卡片 → 左栏组 = 加入分组;组标题条 → 右栏作用域 = 整组激活。
// 拖动期间不能整页重绘(被拖的卡片节点一旦被替换,拖放就断了),悬停展开时只重绘右栏。
let aDrag = null; // { kind: 'skill' | 'group', id }
let aHoverEl = null;
let aSpringTimer = null;

function aDragStart(e, kind, id) {
  aDrag = { kind, id };
  e.dataTransfer.effectAllowed = 'copy';
  e.dataTransfer.setData('text/plain', `${kind}:${id}`);
  const ghost = document.getElementById('drag-ghost');
  ghost.textContent = kind === 'skill' ? skillById(id).name : `组「${groupById(id).name}」· ${groupById(id).members.length} 个技能`;
  e.dataTransfer.setDragImage(ghost, 12, 12);
  // 在 dragstart 里同步改 DOM 会让部分引擎取消拖动,推迟一帧再高亮目标。
  setTimeout(() => {
    document.querySelector('.a-body')?.classList.add('dragging', 'drag-' + kind);
    if (!A.right) { A.right = true; aRerenderRight(); }
    aMarkTargets();
  });
}

function aDragEnd() {
  aDrag = null;
  clearTimeout(aSpringTimer);
  aHoverEl?.classList.remove('drop-hover');
  aHoverEl = null;
  document.querySelector('.a-body')?.classList.remove('dragging', 'drag-skill', 'drag-group');
}

// 标出"已经在那里"的目标:技能已在该作用域 / 组已整组激活 / 技能已在该组。
function aMarkTargets() {
  if (!aDrag) return;
  for (const el of document.querySelectorAll('[data-drop-scope]')) {
    const sc = parseScope(el.dataset.dropScope);
    const already = aDrag.kind === 'skill' ? !!activationIn(aDrag.id, sc)?.individual : groupStateIn(groupById(aDrag.id), sc) === 'on';
    el.classList.toggle('already', already);
  }
  for (const el of document.querySelectorAll('[data-drop-group]')) {
    el.classList.toggle('already', aDrag.kind === 'skill' && groupById(+el.dataset.dropGroup).members.includes(aDrag.id));
  }
}

function aRerenderRight() {
  const el = document.getElementById('a-right');
  if (!el) return;
  const tmp = document.createElement('div');
  tmp.innerHTML = renderA();
  el.replaceWith(tmp.querySelector('#a-right'));
  aMarkTargets();
}

const aTarget = e => e.target.closest?.('[data-drop-scope],[data-drop-group],[data-spring]');

document.addEventListener('dragover', e => {
  if (!aDrag) return;
  const t = aTarget(e);
  if (t !== aHoverEl) {
    aHoverEl?.classList.remove('drop-hover');
    clearTimeout(aSpringTimer);
    aHoverEl = t;
    if (t && !(t.dataset.dropGroup && aDrag.kind === 'group')) t.classList.add('drop-hover');
    // 悬停展开:折叠的项目行 / "其他 Agent"行停 500ms 后展开,只重绘右栏。
    if (t?.dataset.spring) {
      aSpringTimer = setTimeout(() => {
        const [kind, id] = t.dataset.spring.split(':');
        if (kind === 'proj') A.expanded.add(+id); else A.moreAgents.add(+id);
        aHoverEl = null;
        aRerenderRight();
      }, 500);
    }
  }
  if (!t || t.dataset.spring || (t.dataset.dropGroup && aDrag.kind === 'group')) return;
  e.preventDefault();
  e.dataTransfer.dropEffect = 'copy';
});

document.addEventListener('drop', async e => {
  if (!aDrag) return;
  e.preventDefault();
  const t = aTarget(e);
  const d = aDrag;
  aDragEnd();
  if (t?.dataset.dropScope) {
    const sc = parseScope(t.dataset.dropScope);
    if (d.kind === 'skill') {
      const name = skillById(d.id).name;
      const act = activationIn(d.id, sc);
      if (act?.individual) return toast(`${name} 已在 ${scopeLabel(sc)} 独立激活`);
      if (await mutate('Activate', d.id, sc)) toast(act ? `${name} 已在 ${scopeLabel(sc)} 经由分组激活,现追加独立激活` : `已把 ${name} 激活到 ${scopeLabel(sc)}`);
    } else {
      const g = groupById(d.id);
      if (groupStateIn(g, sc) === 'on') return toast(`「${g.name}」已整组激活在 ${scopeLabel(sc)}`);
      if (await mutate('ActivateGroup', d.id, sc)) toast(`已把「${g.name}」整组激活到 ${scopeLabel(sc)}(${g.members.length} 个技能)`);
    }
  } else if (t?.dataset.dropGroup && d.kind === 'skill') {
    const g = groupById(+t.dataset.dropGroup);
    const name = skillById(d.id).name;
    if (g.members.includes(d.id)) return toast(`${name} 已在「${g.name}」中`);
    if (await mutate('AddSkillToGroup', g.id, d.id)) toast(`已把 ${name} 加入「${g.name}」(不会自动激活到该组已激活的作用域)`);
  }
});

document.addEventListener('keydown', e => {
  if (current !== 'A') return;
  if (e.key === 'Escape' && A.scopeMenu) return aSet({ scopeMenu: false });
  if (e.key === 'Escape' && A.drawer) return aSet({ drawer: null });
  if (e.key === 'k' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); document.getElementById('a-search')?.focus(); }
});
