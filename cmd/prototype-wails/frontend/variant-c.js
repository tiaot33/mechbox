// 变体 C · 矩阵总览:一屏看清"哪个技能在哪个作用域、为什么在、是否最新"。
// 行 = 技能(可按集合 / 组 / 平铺分节),列 = 全局与每个已登记项目 × Agent。
// 单元格点击 = 切换独立激活;按组分节时,节标题行的单元格 = 整组激活开关。
const C = { by: 'group', q: '', issuesOnly: false, selected: null, folded: new Set() };

VARIANTS.C = { name: '矩阵总览', render: renderC };

function cSet(patch) { Object.assign(C, patch); render(); }
function cFold(key) { C.folded.has(key) ? C.folded.delete(key) : C.folded.add(key); render(); }

function cSections() {
  let skills = S.skills;
  if (C.q) skills = skills.filter(s => (s.name + s.description).toLowerCase().includes(C.q.toLowerCase()));
  if (C.issuesOnly) skills = skills.filter(s => s.update_available || activationsOf(s.id).some(a => a.status !== 'current'));
  const ids = new Set(skills.map(s => s.id));
  const pick = list => list.filter(id => ids.has(id)).map(skillById);
  if (C.by === 'flat') return [{ key: 'all', title: '全部技能', skills }];
  if (C.by === 'collection') return S.collections.map(c => ({
    key: 'c' + c.source.id, title: sourceLabel(c.source), sub: `${SOURCE_KIND[c.source.type]} · ${sourceRef(c.source)}`,
    icon: SOURCE_ICON[c.source.type], skills: pick(c.skills.map(x => x.id)),
  })).filter(s => s.skills.length);
  const grouped = new Set(S.groups.flatMap(g => g.members));
  return [
    ...S.groups.map(g => ({ key: 'g' + g.id, title: g.name, icon: I.layers, group: g, skills: pick(g.members) })),
    { key: 'none', title: '未分组', skills: skills.filter(s => !grouped.has(s.id)) },
  ].filter(s => s.skills.length || s.group);
}

function renderC() {
  const scopes = allScopes();
  const colHeads = `
    <tr class="c-h1"><th class="c-sticky" rowspan="2"></th>
      <th colspan="${S.agents.length}" class="c-colgroup">${I.globe} 全局</th>
      ${S.projects.map(p => `<th colspan="${S.agents.length}" class="c-colgroup" title="${esc(p.path)}">${I.folder} ${esc(projectName(p.id))}</th>`).join('')}
    </tr>
    <tr class="c-h2">${scopes.map((sc, i) => `<th class="${i % S.agents.length === 0 ? 'c-first' : ''}"><span class="agent-mark ${sc.agent}"></span>${esc(agentName(sc.agent))}</th>`).join('')}</tr>`;

  const cell = (s, sc, i) => {
    const a = activationIn(s.id, sc);
    const key = scopeKey(sc);
    let cls = '', tip = `${scopeLabel(sc)}:未激活,点击独立激活`;
    if (a) {
      cls = [a.individual ? 'ind' : '', a.via_groups.length ? 'grp' : '', a.status !== 'current' ? STATUS[a.status].tone : ''].join(' ');
      tip = `${scopeLabel(sc)}:${a.individual ? '独立激活' : ''}${a.via_groups.length ? `${a.individual ? ' + ' : ''}经由 ${a.via_groups.map(g => groupById(g).name).join('、')}` : ''} · ${STATUS[a.status].label}`
        + (a.individual ? '\n点击:撤回独立激活' : '\n点击:追加独立激活');
    }
    return `<td class="${i % S.agents.length === 0 ? 'c-first' : ''}"><button class="c-cell ${cls}" title="${esc(tip)}" onclick="toggleIndividual(${s.id},'${key}')"><i></i></button></td>`;
  };

  const groupCell = (g, sc, i) => {
    const st = groupStateIn(g, sc);
    return `<td class="${i % S.agents.length === 0 ? 'c-first' : ''}"><button class="c-gcell ${st}" title="${esc(`「${g.name}」在 ${scopeLabel(sc)}:${st === 'on' ? '已整组激活,点击停用' : st === 'partial' ? '部分激活,点击补齐' : '未激活,点击整组激活'}`)}" onclick="toggleGroup(${g.id},'${scopeKey(sc)}')">${st === 'on' ? '整组' : st === 'partial' ? '部分' : '＋'}</button></td>`;
  };

  const body = cSections().map(sec => {
    const folded = C.folded.has(sec.key);
    const head = C.by === 'flat' ? '' : `<tr class="c-sec">
      <th class="c-sticky" onclick="cFold('${sec.key}')"><div class="c-sec-title"><span class="chev ${folded ? '' : 'open'}">${I.chev}</span>${sec.icon || ''}<span>${esc(sec.title)}</span><span class="count">${sec.skills.length}</span>${sec.sub ? `<span class="tiny muted mono">${esc(sec.sub)}</span>` : ''}</div></th>
      ${sec.group ? scopes.map((sc, i) => groupCell(sec.group, sc, i)).join('') : scopes.map((sc, i) => { const n = sec.skills.filter(s => activationIn(s.id, sc)).length; return `<td class="${i % S.agents.length === 0 ? 'c-first' : ''}"><span class="tiny muted">${n || ''}</span></td>`; }).join('')}
    </tr>`;
    if (folded) return head;
    return head + sec.skills.map(s => `<tr class="${C.selected === s.id ? 'selected' : ''}">
      <th class="c-sticky c-skill" onclick="cSet({selected:${C.selected === s.id ? 'null' : s.id}})">
        <span class="c-name">${esc(s.name)}</span>${s.update_available ? '<span class="badge warn">可更新</span>' : ''}
        <span class="tiny muted ellipsis">${esc(C.by === 'collection' ? s.description : sourceLabel(s.source))}</span>
      </th>${scopes.map((sc, i) => cell(s, sc, i)).join('')}</tr>`).join('');
  }).join('');

  const issues = S.activations.filter(a => a.status !== 'current').length;
  const updates = S.skills.filter(s => s.update_available).length;

  return `
    ${titlebar('', '<span class="tb-title">技能 × 作用域</span>', `<button class="pill-btn" onclick="stub('安装技能')">${I.plus}安装技能</button>`)}
    <div class="c-body">
      <main class="c-main tile">
        <div class="c-toolbar">
          <label class="search">${I.search}<input placeholder="搜索技能…" value="${esc(C.q)}" onchange="cSet({q:this.value})" onkeydown="if(event.key==='Enter')cSet({q:this.value})"></label>
          <div class="seg">
            <span class="seg-label">分节</span>
            <button class="${C.by === 'group' ? 'on' : ''}" onclick="cSet({by:'group'})">按组</button>
            <button class="${C.by === 'collection' ? 'on' : ''}" onclick="cSet({by:'collection'})">按集合</button>
            <button class="${C.by === 'flat' ? 'on' : ''}" onclick="cSet({by:'flat'})">平铺</button>
          </div>
          <label class="toggle-label">${switchHtml(C.issuesOnly, 'cSet({issuesOnly:!C.issuesOnly})')}<span>只看需处理</span></label>
          <span class="grow"></span>
          <span class="stat ${issues ? 'warn' : ''}"><b>${issues}</b> 条投影需重写</span>
          <span class="stat ${updates ? 'warn' : ''}"><b>${updates}</b> 个来源有更新</span>
          ${C.by === 'group' ? `<button class="pill-btn" onclick="newGroup()">${I.plus}新建分组</button>` : ''}
        </div>
        <div class="c-legend">
          <span><button class="c-cell ind"><i></i></button>独立激活</span>
          <span><button class="c-cell grp"><i></i></button>经由分组</span>
          <span><button class="c-cell ind grp"><i></i></button>两者都有</span>
          <span><button class="c-cell ind warn"><i></i></button>旧版本</span>
          <span><button class="c-cell ind bad"><i></i></button>被改动 / 缺失</span>
          <span class="muted">按组分节时,节标题行的格子是整组开关</span>
        </div>
        <div class="c-scroll" data-keep-scroll="c-scroll">
          <table class="c-table">${colHeads}${body}</table>
        </div>
      </main>
      ${C.selected ? `<aside class="c-sheet tile" data-keep-scroll="c-sheet">
        <div class="drawer-head"><span class="muted">技能详情</span><span class="grow"></span><button class="icon-btn" onclick="cSet({selected:null})">${I.close}</button></div>
        ${renderDetail(C.selected)}
      </aside>` : ''}
    </div>`;
}
