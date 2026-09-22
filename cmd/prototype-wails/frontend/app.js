// PROTOTYPE 公共层:API 桥接、快照状态、领域标签、共享的技能详情块、变体切换条。
// 各变体只共享这些"零件",布局各自负责。

const IS_WAILS = !location.protocol.startsWith('http');
document.documentElement.classList.add(IS_WAILS ? 'is-wails' : 'is-browser');

// ---------- API:Wails 走 window.go 绑定,浏览器模式走 /call/<方法> ----------
async function api(method, ...args) {
  if (IS_WAILS) {
    for (let i = 0; !window.go?.main?.App && i < 100; i++) await new Promise(r => setTimeout(r, 20));
    return window.go.main.App[method](...args);
  }
  const res = await fetch('/call/' + method, { method: 'POST', body: JSON.stringify(args) });
  const body = await res.json();
  if (body.error) throw new Error(body.error);
  return body.result;
}

// 调用一个会改状态的方法,成功后重新拉快照并重绘当前变体;失败弹出错误。
async function mutate(method, ...args) {
  try {
    await api(method, ...args);
    await reload();
    return true;
  } catch (e) {
    toast(String(e.message || e), 'error');
    return false;
  }
}

let S = null; // 最近一次 Snapshot
async function reload() {
  S = await api('Snapshot');
  render();
}

// ---------- 领域辅助 ----------
const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const skillById = id => S.skills.find(s => s.id === id);
const groupById = id => S.groups.find(g => g.id === id);
const agentName = code => S.agents.find(a => a.code === code)?.name ?? code;
const projectById = id => S.projects.find(p => p.id === id);
const projectName = id => (projectById(id)?.path ?? '').split('/').pop();
const shortPath = p => p.replace(/^\/Users\/[^/]+/, '~');

// Scope 身份:Agent 全局,或 Agent + 已登记项目。key 形如 "claude-code@0"。
const scopeKey = sc => `${sc.agent}@${sc.project_id || 0}`;
const parseScope = key => { const [agent, p] = key.split('@'); return +p ? { agent, project_id: +p } : { agent }; };
function allScopes() {
  const out = [];
  for (const a of S.agents) out.push({ agent: a.code });
  for (const p of S.projects) for (const a of S.agents) out.push({ agent: a.code, project_id: p.id });
  return out;
}
const scopeLabel = sc => sc.project_id ? `${agentName(sc.agent)} · ${projectName(sc.project_id)}` : `${agentName(sc.agent)} · 全局`;
const activationsOf = skillId => S.activations.filter(a => a.skill_id === skillId);
const activationIn = (skillId, sc) => S.activations.find(a => a.skill_id === skillId && scopeKey(a.scope) === scopeKey(sc));
const activationsInScope = sc => S.activations.filter(a => scopeKey(a.scope) === scopeKey(sc));

// 分组在某个作用域的激活程度:以"该组是否为成员贡献了来源"为准。
function groupStateIn(group, sc) {
  const via = group.members.filter(id => activationIn(id, sc)?.via_groups.includes(group.id)).length;
  if (group.members.length && via === group.members.length) return 'on';
  return via ? 'partial' : 'off';
}

const SOURCE_KIND = { github: 'GitHub', url: 'URL', store: '商店', local: '自研' };
function sourceLabel(src) {
  const l = src.locator;
  switch (src.type) {
    case 'github': return l.repository.replace('https://github.com/', '');
    case 'store': return l.package_id;
    case 'url': return l.url.split('/').pop();
    case 'local': return shortPath(l.path);
  }
}
function sourceRef(src) {
  const l = src.locator;
  if (src.type === 'github') return `${l.branch} · ${src.last_seen_ref}`;
  if (src.type === 'store') return `v${l.version}`;
  if (src.type === 'url') return new URL(l.url).host;
  return '本地目录';
}
// 卡片 / 芯片上用的短名:GitHub 取 owner,商店取包名,URL 取文件名,本地来源统一叫"自研"。
function sourceShort(src) {
  const l = src.locator;
  if (src.type === 'github') return l.repository.replace('https://github.com/', '').split('/')[0];
  if (src.type === 'store') return l.package_id.split('/').pop();
  if (src.type === 'url') return l.url.split('/').pop().replace(/\.zip$/, '');
  return '自研';
}

const STATUS = {
  current: { label: '最新', tone: 'ok' },
  stale: { label: '旧版本', tone: 'warn' },
  modified: { label: '已被改动', tone: 'bad' },
  missing: { label: '目录缺失', tone: 'bad' },
  directory_missing: { label: 'Scope 目录缺失', tone: 'bad' },
};

function provenance(act) {
  const parts = [];
  if (act.individual) parts.push('<span class="prov prov-ind">独立</span>');
  for (const g of act.via_groups) parts.push(`<span class="prov prov-grp">经由「${esc(groupById(g)?.name)}」</span>`);
  return parts.join('');
}

const fmtDate = t => { const d = new Date(t); return isNaN(d) || d.getFullYear() < 2000 ? '未知' : `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`; };
function ago(t) {
  const days = Math.round((Date.parse('2026-09-23T10:00:00') - Date.parse(t)) / 864e5);
  return days <= 0 ? '今天' : days < 30 ? `${days} 天前` : `${Math.round(days / 30)} 个月前`;
}

// ---------- 图标 ----------
const I = {
  plus: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>',
  search: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>',
  folder: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>',
  globe: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>',
  bolt: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/></svg>',
  layers: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><polygon points="12 2 2 7 12 12 22 7 12 2"/><polyline points="2 17 12 22 22 17"/><polyline points="2 12 12 17 22 12"/></svg>',
  tag: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/></svg>',
  gear: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><circle cx="12" cy="12" r="3"/><path d="M12 1v3M12 20v3M4.2 4.2l2.1 2.1M17.7 17.7l2.1 2.1M1 12h3M20 12h3M4.2 19.8l2.1-2.1M17.7 6.3l2.1-2.1"/></svg>',
  cloud: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M18 10h-1.26A8 8 0 1 0 9 20h9a5 5 0 0 0 0-10z"/></svg>',
  up: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="17 11 12 6 7 11"/><line x1="12" y1="6" x2="12" y2="18"/></svg>',
  sidebarL: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="18" height="18" rx="2"/><line x1="9" y1="3" x2="9" y2="21"/></svg>',
  sidebarR: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="18" height="18" rx="2"/><line x1="15" y1="3" x2="15" y2="21"/></svg>',
  close: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>',
  chev: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="9 18 15 12 9 6"/></svg>',
  refresh: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="23 4 23 10 17 10"/><path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/></svg>',
};
const SOURCE_ICON = {
  github: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><circle cx="6" cy="6" r="2.5"/><circle cx="6" cy="18" r="2.5"/><circle cx="18" cy="8" r="2.5"/><path d="M6 8.5v7M18 10.5c0 4-6 3-10.5 6"/></svg>',
  store: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M3 9l1.5-5h15L21 9"/><path d="M4 9v11h16V9"/><path d="M3 9h18"/></svg>',
  url: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/></svg>',
  local: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z"/></svg>',
};

// ---------- 通用小部件 ----------
function toast(msg, tone = 'ok') {
  const el = document.getElementById('toast');
  el.textContent = msg;
  el.className = 'toast show ' + tone;
  clearTimeout(toast.t);
  toast.t = setTimeout(() => (el.className = 'toast ' + tone), tone === 'error' ? 4200 : 2200);
}
const stub = what => toast(`「${what}」不在本原型范围内`);

// 标题栏:Wails 下让出原生红绿灯的位置并作为拖拽区;浏览器下画假红绿灯。
function titlebar(left = '', center = '', right = '') {
  return `<header class="titlebar" style="--wails-draggable:drag">
    <div class="tb-left">${IS_WAILS ? '<div class="tl-space"></div>' : '<div class="traffic"><i></i><i></i><i></i></div>'}${left}</div>
    <div class="tb-center">${center}</div>
    <div class="tb-right">${right}</div>
  </header>`;
}

function renderMarkdown(md) {
  md = md.replace(/^---\n[\s\S]*?\n---\n/, '');
  const inline = s => esc(s).replace(/`([^`]+)`/g, '<code>$1</code>').replace(/\*\*([^*]+)\*\*/g, '<b>$1</b>');
  let html = '', list = null;
  const close = () => { if (list) { html += `</${list}>`; list = null; } };
  for (const line of md.split('\n')) {
    let m;
    if ((m = line.match(/^(#{1,3}) (.*)/))) { close(); html += `<h${m[1].length + 2}>${inline(m[2])}</h${m[1].length + 2}>`; }
    else if ((m = line.match(/^- (.*)/))) { if (list !== 'ul') { close(); html += '<ul>'; list = 'ul'; } html += `<li>${inline(m[1])}</li>`; }
    else if ((m = line.match(/^\d+\. (.*)/))) { if (list !== 'ol') { close(); html += '<ol>'; list = 'ol'; } html += `<li>${inline(m[1])}</li>`; }
    else if (line.trim()) { close(); html += `<p>${inline(line)}</p>`; }
    else close();
  }
  close();
  return html;
}

// 共享的技能详情块。opts.scopes = true 时附带"各作用域激活"表(变体自行决定要不要)。
const detailCache = new Map();
async function loadDetail(id) {
  const d = await api('GetSkill', id);
  detailCache.set(id, d);
  return d;
}
function renderDetail(id, opts = {}) {
  const skill = skillById(id);
  const d = detailCache.get(id);
  if (!d) { loadDetail(id).then(render); return '<div class="detail-loading">加载中…</div>'; }
  const src = skill.source;
  const inGroups = S.groups.filter(g => g.members.includes(id));
  const notIn = S.groups.filter(g => !g.members.includes(id));
  const scopesTable = opts.scopes ? `
    <section class="card">
      <div class="card-h"><span>激活</span><span class="muted">${activationsOf(id).length} 个作用域</span></div>
      <table class="scope-table">
        <thead><tr><th></th><th>作用域</th><th>来源</th><th>状态</th></tr></thead>
        <tbody>${allScopes().map(sc => {
          const act = activationIn(id, sc);
          const st = act && STATUS[act.status];
          return `<tr class="${act ? '' : 'dim'}">
            <td>${switchHtml(!!act?.individual, `toggleIndividual(${id},'${scopeKey(sc)}')`, act && !act.individual ? 'group-held' : '')}</td>
            <td><div>${esc(scopeLabel(sc))}</div>${act ? `<div class="mono tiny muted">${esc(shortPath(act.directory))}</div>` : ''}</td>
            <td>${act ? provenance(act) : '<span class="muted">—</span>'}</td>
            <td>${st ? `<span class="status ${st.tone}">${st.label}</span>${act.status !== 'current' ? ` <button class="link" onclick="mutate('Refresh',${id},${JSON.stringify(sc).replace(/"/g, '&quot;')})">重写</button>` : ''}` : ''}</td>
          </tr>`;
        }).join('')}</tbody>
      </table>
      <p class="hint">开关只控制<b>独立激活</b>;经由分组的激活需在分组上停用。</p>
    </section>` : '';
  return `
    <div class="detail">
      <div class="detail-title">
        <h2>${esc(skill.name)}</h2>
        ${skill.update_available ? `<button class="pill-btn accent" onclick="mutate('Update',${id})">${I.up}更新到新版本</button>` : ''}
      </div>
      <p class="detail-desc">${esc(skill.description)}</p>
      <section class="card">
        <div class="card-h"><span>元数据</span></div>
        <dl class="meta">
          <dt>集合</dt><dd><span class="src-chip">${SOURCE_ICON[src.type]}${esc(sourceLabel(src))}</span> <span class="muted">${SOURCE_KIND[src.type]}</span></dd>
          <dt>来源版本</dt><dd class="mono">${esc(sourceRef(src))}</dd>
          <dt>安装于</dt><dd>${fmtDate(skill.installed_at)}</dd>
          <dt>当前版本</dt><dd>${fmtDate(skill.updated_at)} · 共 ${skill.versions} 个版本${skill.update_available ? ' · <span class="status warn">来源有更新</span>' : ''}</dd>
          <dt>文件</dt><dd class="files">${d.skill.files.map(f => `<span class="mono">${esc(f.path)}${f.mode === 'executable' ? ' <i class="muted">x</i>' : ''}</span>`).join('')}</dd>
          <dt>组</dt><dd class="chips">${inGroups.map(g => `<span class="chip">${esc(g.name)}<button onclick="mutate('RemoveSkillFromGroup',${g.id},${id})" title="移出分组">×</button></span>`).join('')}
            ${notIn.length ? `<select class="chip-add" onchange="this.value && mutate('AddSkillToGroup',+this.value,${id})"><option value="">+ 加入分组</option>${notIn.map(g => `<option value="${g.id}">${esc(g.name)}</option>`).join('')}</select>` : ''}</dd>
        </dl>
      </section>
      ${scopesTable}
      <section class="card">
        <div class="card-h"><span>SKILL.md</span><span class="muted mono tiny">只读 · 内容来自库</span></div>
        <div class="md">${renderMarkdown(new TextDecoder().decode(Uint8Array.from(atob(d.skill.files[0].content), c => c.charCodeAt(0))))}</div>
      </section>
      <section class="card">
        <div class="card-h"><span>版本历史</span></div>
        <ol class="versions">${d.versions.map((v, i) => `<li><span class="mono">${esc(v.git_tree_sha || '—')}</span><span>${fmtDate(v.created_at)}</span>${i === 0 ? '<span class="status ok">当前</span>' : ''}</li>`).join('')}</ol>
      </section>
    </div>`;
}

function switchHtml(on, onclick, cls = '') {
  return `<button class="switch ${on ? 'on' : ''} ${cls}" onclick="event.stopPropagation();${onclick}" role="switch" aria-checked="${on}"><i></i></button>`;
}

// 独立激活开关:开 → Activate(已经由分组激活时也是追加独立来源);关 → Deactivate。
function toggleIndividual(id, key) {
  const sc = parseScope(key);
  return activationIn(id, sc)?.individual ? mutate('Deactivate', id, sc) : mutate('Activate', id, sc);
}
// "停用"按钮:不区分来源地请求停用;仅分组来源时由桩返回 FailedPrecondition,原样提示给用户。
const deactivate = (id, key) => mutate('Deactivate', id, parseScope(key));
function toggleGroup(gid, key) {
  const sc = parseScope(key);
  return groupStateIn(groupById(gid), sc) === 'on' ? mutate('DeactivateGroup', gid, sc) : mutate('ActivateGroup', gid, sc);
}
async function newGroup() {
  const name = prompt('新分组名称(按场景命名,例如"前端调试")');
  if (name) await mutate('CreateGroup', name);
}

// ---------- 变体注册与切换条 ----------
const VARIANTS = {}; // key -> { name, render(root) }
const variantKeys = () => Object.keys(VARIANTS);
let current = new URLSearchParams(location.search).get('variant') || localStorage.getItem('proto-variant') || 'A';

function setVariant(key) {
  current = key;
  localStorage.setItem('proto-variant', key);
  const url = new URL(location.href);
  url.searchParams.set('variant', key);
  history.replaceState(null, '', url);
  render();
}
function cycle(step) {
  const keys = variantKeys();
  setVariant(keys[(keys.indexOf(current) + step + keys.length) % keys.length]);
}
document.addEventListener('keydown', e => {
  const t = e.target;
  if (t.closest?.('input, textarea, select, [contenteditable]')) return;
  if (e.key === 'ArrowLeft') cycle(-1);
  if (e.key === 'ArrowRight') cycle(1);
});

function render() {
  if (!S) return;
  if (!VARIANTS[current]) current = variantKeys()[0];
  const root = document.getElementById('root');
  const scroll = [...root.querySelectorAll('[data-keep-scroll]')].map(el => [el.dataset.keepScroll, el.scrollTop]);
  root.className = 'window variant-' + current;
  root.innerHTML = VARIANTS[current].render();
  for (const [k, top] of scroll) { const el = root.querySelector(`[data-keep-scroll="${k}"]`); if (el) el.scrollTop = top; }
  document.getElementById('switcher').innerHTML = `
    <button onclick="cycle(-1)" title="上一个变体 (←)">‹</button>
    <span><b>${current}</b> ${esc(VARIANTS[current].name)}</span>
    <button onclick="cycle(1)" title="下一个变体 (→)">›</button>`;
}

window.addEventListener('DOMContentLoaded', reload);
