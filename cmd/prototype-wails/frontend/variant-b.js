// 变体 B · 作用域优先:用户的主问题是"这个 Agent / 这个项目里现在有哪些技能"。
// 左栏就是作用域树(全局 + 已登记项目 × Agent);主区对当前作用域依次给出
// 组(整组开关)→ 已激活技能(来源、健康状态、停用)→ 从库中添加。详情固定在右侧,不遮挡列表。
const B = { scope: 'claude-code@0', selected: null, q: '', coll: 0, library: false };

VARIANTS.B = { name: '作用域优先', render: renderB };

function bSet(patch) { Object.assign(B, patch); render(); }

// 切换地方时,沿用当前 Agent(若它在新地方也在用),否则取新地方第一个在用的 Agent。
function bPlace(place) {
  const agent = parseScope(B.scope).agent;
  const used = agentsUsedIn(place).map(a => a.code);
  const next = used.includes(agent) ? agent : used[0] || agent;
  bSet({ scope: scopeKey(place ? { agent: next, project_id: place } : { agent: next }), library: false });
}
function bAgent(code) {
  const sc = parseScope(B.scope);
  bSet({ scope: scopeKey({ ...sc, agent: code }) });
}

// 第二步:主区顶部选 Agent。只给在用的 Agent 开标签,其余收进下拉。
function bAgentTabs(sc) {
  const place = sc.project_id || 0;
  const used = agentsUsedIn(place);
  const tabs = used.some(a => a.code === sc.agent) ? used : [...used, S.agents.find(a => a.code === sc.agent)];
  const rest = S.agents.filter(a => !tabs.includes(a));
  return `<div class="b-tabs">
    ${tabs.map(a => {
      const acts = activationsInScope({ ...sc, agent: a.code });
      const issues = acts.filter(x => x.status !== 'current').length;
      return `<button class="b-tab ${a.code === sc.agent ? 'on' : ''}" onclick="bAgent('${a.code}')">${agentMark(a.code)}${esc(a.name)}
        ${issues ? `<span class="status warn">${issues}</span>` : ''}<span class="count">${acts.length}</span></button>`;
    }).join('')}
    ${rest.length ? `<select class="b-more" onchange="this.value && bAgent(this.value)">
      <option value="">+ 其他 ${rest.length} 个 Agent</option>${rest.map(a => `<option value="${a.code}">${esc(a.name)}</option>`).join('')}</select>` : ''}
  </div>`;
}

function renderB() {
  // 第一步:左栏只选"地方"(全局或某个项目),行数只随项目数增长,与 Agent 数量无关。
  const place = +(parseScope(B.scope).project_id || 0);
  const placeItem = (p, icon, label, title = '') => {
    const acts = S.activations.filter(a => (a.scope.project_id || 0) === p);
    const issues = acts.filter(a => a.status !== 'current').length;
    return `<li class="nav-item b-place ${!B.library && place === p ? 'active' : ''}" onclick="bPlace(${p})" title="${esc(title)}">
      ${icon}<span class="grow ellipsis">${esc(label)}</span>
      <span class="marks">${agentsUsedIn(p).map(a => agentMark(a.code)).join('')}</span>
      ${issues ? `<span class="status warn">${issues}</span>` : ''}<span class="count">${acts.length}</span></li>`;
  };
  const left = `
    <aside class="b-left">
      <ul class="nav">${placeItem(0, I.globe, '全局', '~')}</ul>
      <div class="nav-sec"><span>已登记项目</span></div>
      <ul class="nav">${S.projects.map(p => placeItem(p.id, I.folder, projectName(p.id), p.path)).join('')}</ul>
      <button class="side-btn ghost" onclick="stub('登记项目')">${I.plus}<span>登记项目</span></button>
      <div class="grow"></div>
      <ul class="nav">
        <li class="nav-item ${B.library ? 'active' : ''}" onclick="bSet({library:true})">${I.bolt}<span class="grow">技能库</span><span class="count">${S.skills.length}</span></li>
        <li class="nav-item" onclick="stub('S3 同步')">${I.cloud}<span class="grow">同步</span></li>
        <li class="nav-item" onclick="stub('设置')">${I.gear}<span class="grow">设置</span></li>
      </ul>
    </aside>`;

  const main = B.library ? bLibrary() : bScope();
  const side = B.selected ? `
    <aside class="b-detail tile" data-keep-scroll="b-detail">
      <div class="drawer-head"><span class="muted">技能详情</span><span class="grow"></span><button class="icon-btn" onclick="bSet({selected:null})">${I.close}</button></div>
      ${bAlsoIn(B.selected)}
      ${renderDetail(B.selected)}
    </aside>` : '';

  const title = B.library ? '技能库' : scopeLabel(parseScope(B.scope));
  return `
    ${titlebar('', `<span class="tb-title">${esc(title)}</span>`, `<button class="pill-btn" onclick="stub('安装技能')">${I.plus}安装技能</button>`)}
    <div class="b-body">${left}${main}${side}</div>`;
}

// 当前作用域工作台。
function bScope() {
  const sc = parseScope(B.scope);
  const acts = activationsInScope(sc).sort((x, y) => skillById(x.skill_id).name.localeCompare(skillById(y.skill_id).name));
  const issues = acts.filter(a => a.status !== 'current');
  const dir = scopeDir(sc);

  const groups = S.groups.map(g => {
    const st = groupStateIn(g, sc);
    return `<div class="b-group ${st}">
      <div class="b-group-top"><span class="b-group-name">${I.layers}${esc(g.name)}</span>${switchHtml(st === 'on', `toggleGroup(${g.id},'${B.scope}')`, st === 'partial' ? 'partial' : '')}</div>
      <div class="b-group-members">${g.members.map(id => `<span class="${activationIn(id, sc) ? 'in' : ''}">${esc(skillById(id).name)}</span>`).join('')}</div>
      <div class="tiny muted">${g.members.length} 个成员 · ${st === 'on' ? '已整组激活' : st === 'partial' ? '部分激活' : '未激活'}</div>
    </div>`;
  }).join('');

  const rows = acts.map(a => {
    const s = skillById(a.skill_id);
    const st = STATUS[a.status];
    return `<tr class="${B.selected === s.id ? 'selected' : ''}" onclick="bSet({selected:${s.id}})">
      <td><b>${esc(s.name)}</b>${s.update_available ? ' <span class="badge warn">可更新</span>' : ''}</td>
      <td class="muted"><span class="src-chip">${SOURCE_ICON[s.source.type]}${esc(sourceLabel(s.source))}</span></td>
      <td>${provenance(a)}</td>
      <td><span class="status ${st.tone}">${st.label}</span></td>
      <td class="right">
        ${a.status !== 'current' ? `<button class="link" onclick="event.stopPropagation();mutate('Refresh',${s.id},parseScope('${B.scope}'))">重写</button>` : ''}
        <button class="link danger" onclick="event.stopPropagation();deactivate(${s.id},'${B.scope}')">停用</button>
      </td></tr>`;
  }).join('');

  const activeIds = new Set(acts.map(a => a.skill_id));
  let pool = S.skills.filter(s => !activeIds.has(s.id));
  if (B.coll) pool = pool.filter(s => s.source.id === B.coll);
  if (B.q) pool = pool.filter(s => (s.name + s.description).toLowerCase().includes(B.q.toLowerCase()));

  return `
    <main class="b-main tile" data-keep-scroll="b-main">
      <div class="b-place-head">
        <h1>${sc.project_id ? esc(projectName(sc.project_id)) : '全局'}</h1>
        <span class="mono tiny muted">${esc(shortPath(sc.project_id ? projectById(sc.project_id).path : '~'))}</span>
      </div>
      ${bAgentTabs(sc)}
      <div class="b-hero">
        <div>
          <h2>${agentMark(sc.agent)} ${esc(agentName(sc.agent))}</h2>
          <div class="mono tiny muted">${esc(dir)}</div>
        </div>
        <div class="b-health">
          <span class="stat"><b>${acts.length}</b> 已激活</span>
          <span class="stat ${issues.length ? 'warn' : ''}"><b>${issues.length}</b> 需重写</span>
          ${issues.length ? `<button class="pill-btn" onclick="bRefreshAll()">${I.refresh}全部重写</button>` : ''}
        </div>
      </div>

      <h3 class="b-sec">组 <span class="muted tiny">整组激活 / 停用;停用只撤回本组的来源</span><button class="icon-btn sm" onclick="newGroup()" title="新建分组">${I.plus}</button></h3>
      <div class="b-groups">${groups}</div>

      <h3 class="b-sec">已激活 <span class="count">${acts.length}</span></h3>
      <table class="b-table"><thead><tr><th>技能</th><th>集合</th><th>激活来源</th><th>状态</th><th></th></tr></thead>
        <tbody>${rows || '<tr><td colspan="5" class="empty">这个作用域还没有激活任何技能</td></tr>'}</tbody></table>

      <h3 class="b-sec">从库中添加</h3>
      <div class="b-pool-head">
        <label class="search">${I.search}<input placeholder="搜索库中技能…" value="${esc(B.q)}" oninput="B.q=this.value;bRerenderPool()"></label>
        <div class="chip-row">
          <button class="chip ${B.coll ? '' : 'on'}" onclick="bSet({coll:0})">全部集合</button>
          ${S.collections.map(c => `<button class="chip ${B.coll === c.source.id ? 'on' : ''}" onclick="bSet({coll:${c.source.id}})">${SOURCE_ICON[c.source.type]}${esc(sourceShort(c.source))}</button>`).join('')}
        </div>
      </div>
      <div class="b-pool" id="b-pool">${pool.map(s => `
        <div class="b-pool-item ${B.selected === s.id ? 'selected' : ''}" onclick="bSet({selected:${s.id}})">
          <div class="grow"><b>${esc(s.name)}</b><div class="tiny muted ellipsis">${esc(s.description)}</div></div>
          <button class="pill-btn accent sm" onclick="event.stopPropagation();mutate('Activate',${s.id},parseScope('${B.scope}'))">${I.plus}激活</button>
        </div>`).join('') || '<div class="empty">没有可添加的技能</div>'}</div>
    </main>`;
}

// 技能库:按集合分节,每行显示它落在哪些作用域。
function bLibrary() {
  return `<main class="b-main tile" data-keep-scroll="b-lib">
    <div class="b-hero"><div><h1>技能库</h1><div class="tiny muted">${S.skills.length} 个技能 · ${S.collections.length} 个集合 · ${S.groups.length} 个组</div></div></div>
    ${S.collections.map(c => `
      <h3 class="b-sec">${SOURCE_ICON[c.source.type]} ${esc(sourceLabel(c.source))} <span class="muted tiny">${SOURCE_KIND[c.source.type]} · ${esc(sourceRef(c.source))}</span></h3>
      <div class="b-pool">${c.skills.map(sum => {
        const s = skillById(sum.id);
        const acts = activationsOf(s.id);
        return `<div class="b-pool-item ${B.selected === s.id ? 'selected' : ''}" onclick="bSet({selected:${s.id}})">
          <div class="grow"><b>${esc(s.name)}</b>${s.update_available ? ' <span class="badge warn">可更新</span>' : ''}<div class="tiny muted ellipsis">${esc(s.description)}</div></div>
          <div class="place-sum">${[0, ...S.projects.map(p => p.id)].map(p => {
            const here = acts.filter(a => (a.scope.project_id || 0) === p);
            if (!here.length) return '';
            const warn = here.some(a => a.status !== 'current');
            return `<span class="${warn ? 'warn' : ''}" title="${esc(here.map(a => agentName(a.scope.agent)).join('、'))}">${p ? esc(projectName(p)) : '全局'}<span class="marks">${here.map(a => agentMark(a.scope.agent)).join('')}</span></span>`;
          }).join('')}</div>
          <span class="tiny muted">${acts.length} 处</span>
        </div>`;
      }).join('')}</div>`).join('')}
  </main>`;
}

// 详情顶部:这个技能还激活在哪些作用域,点击直接跳过去。
function bAlsoIn(id) {
  const acts = activationsOf(id);
  if (!acts.length) return '';
  return `<div class="b-also"><span class="tiny muted">激活于</span>${acts.map(a => {
    const key = scopeKey(a.scope);
    return `<button class="chip ${!B.library && key === B.scope ? 'on' : ''}" onclick="bSet({scope:'${key}',library:false})">${esc(scopeLabel(a.scope))}</button>`;
  }).join('')}</div>`;
}

async function bRefreshAll() {
  const sc = parseScope(B.scope);
  for (const a of activationsInScope(sc).filter(a => a.status !== 'current')) await api('Refresh', a.skill_id, sc);
  await reload();
  toast('已把该作用域的投影全部重写为当前版本');
}

function bRerenderPool() {
  const tmp = document.createElement('div');
  tmp.innerHTML = bScope();
  document.getElementById('b-pool').replaceWith(tmp.querySelector('#b-pool'));
}
