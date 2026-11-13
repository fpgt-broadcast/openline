// OpenLine phone admin: mute people and edit users from a phone.
// Same API and sign-in as admin.html (the token is shared through sessionStorage).
const $ = (id) => document.getElementById(id);
function h(tag, attrs = {}, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k.startsWith('on')) el.addEventListener(k.slice(2).toLowerCase(), v);
    else if (k === 'style' && typeof v === 'object') for (const [sk, sv] of Object.entries(v)) sk.startsWith('--') ? el.style.setProperty(sk, sv) : (el.style[sk] = sv);
    else if (k === 'class') el.className = v;
    else if (k in el && typeof v !== 'string') el[k] = v;
    else el.setAttribute(k, v === true ? '' : v);
  }
  for (const kid of kids.flat()) if (kid !== null && kid !== undefined && kid !== false) el.append(kid);
  return el;
}

const M = { token: sessionStorage.getItem('ol.admin') || '', cfg: null, sessions: [], tab: 'live', q: '', edit: null };

async function api(method, path, body) {
  const r = await fetch('api/' + path, {
    method, headers: { Authorization: 'Bearer ' + M.token, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (r.status === 401) { sessionStorage.removeItem('ol.admin'); location.reload(); throw new Error('Signed out'); }
  if (!r.ok) throw new Error((await r.text()).trim() || r.statusText);
  return r.json();
}

function toast(text, kind = 'warn', ms = 3000) {
  const t = $('toast');
  t.textContent = text; t.className = 'banner toast ' + kind;
  clearTimeout(toast.t); toast.t = setTimeout(() => t.classList.add('hidden'), ms);
}
const run = (f) => async (...a) => { try { await f(...a); } catch (e) { toast(e.message, 'bad', 5000); } };

// Two-tap confirm instead of the browser's yes/no popup: the first tap arms the
// button for 3 s ("Tap again to …"), the second does it. Native popups are
// unreliable in Safari, especially on iPhone. Arming survives the 2 s re-renders.
const ARM = { key: '', until: 0 };
function sure(key, label, armedLabel, fn, attrs = {}) {
  const armed = ARM.key === key && Date.now() < ARM.until;
  return h('button', { ...attrs, class: ((attrs.class || '') + (armed ? ' armed' : '')).trim(), onclick: run(async (e) => {
    const b = e.currentTarget;
    if (ARM.key === key && Date.now() < ARM.until) { ARM.key = ''; b.classList.remove('armed'); b.textContent = label; return fn(); }
    ARM.key = key; ARM.until = Date.now() + 3000;
    b.textContent = armedLabel; b.classList.add('armed');
    setTimeout(() => {
      if (ARM.key === key && Date.now() >= ARM.until) ARM.key = '';
      if (b.isConnected && ARM.key !== key) { b.textContent = label; b.classList.remove('armed'); }
    }, 3050);
  }) }, armed ? armedLabel : label);
}

const chOf = (id) => M.cfg.channels.find((c) => c.id === id);
const chName = (id) => chOf(id)?.name || '#' + id;
const chColor = (id) => chOf(id)?.color || '#666';
const isBridge = (s) => s.client === 'bridge';

// ---------------- Live ----------------
const remote = (sid, op) => api('POST', `sessions/${sid}/remote`, op);

async function muteAll(on) {
  const list = M.sessions.filter((s) => !isBridge(s) && s.muted !== on);
  if (!list.length) return toast(on ? 'Everyone is already muted.' : 'Nobody is muted.');
  await Promise.all(list.map((s) => remote(s.sid, { op: 'mute', on })));
  await refresh();
  toast(on ? 'All mics muted.' : 'All mics unmuted.', 'ok');
}

function sessCard(s) {
  const name = h('div', { class: 'who' }, h('b', {}, s.user), s.person ? h('span', {}, s.person) : null,
    s.sending ? h('span', { class: 'pill bad' }, 'TALKING') : null,
    s.muted ? h('span', { class: 'pill bad' }, 'MIC MUTED') : null,
    s.tally === 'program' ? h('span', { class: 'pill bad' }, 'ON AIR') : s.tally === 'preview' ? h('span', { class: 'pill ok' }, 'PREVIEW') : null);
  const chips = h('div', { class: 'chs' }, s.channels.map((id) => {
    const t = s.talk.includes(id), l = s.listen.includes(id);
    return h('span', { class: 'chip' + (t ? ' talk' : ''), style: { borderLeft: `3px solid ${chColor(id)}` } },
      chName(id), t ? ' · talk' : l ? '' : ' · off');
  }));
  const acts = isBridge(s) ? h('div', { class: 'small muted' }, 'NDI bridge') : h('div', { class: 'acts' },
    h('button', { class: 'mute' + (s.muted ? ' on' : ''), onclick: run(async () => { await remote(s.sid, { op: 'mute', on: !s.muted }); await refresh(); }) },
      s.muted ? 'Unmute mic' : 'Mute mic'),
    sure('kick' + s.sid, 'Kick', 'Tap again', async () => { await remote(s.sid, { op: 'kick' }); await refresh(); }, { class: 'ghost' }));
  return h('div', { class: 'sess' + (s.sending ? ' talking' : '') }, name,
    h('div', { class: 'small muted' }, `${s.client}${s.device ? ' · ' + s.device : ''}`), chips, acts);
}

function renderLive() {
  const body = $('body');
  if (!body.querySelector('#liveList')) {
    body.replaceChildren(
      h('div', { class: 'bar' },
        h('input', { type: 'search', placeholder: 'Search…', value: M.q, 'aria-label': 'Search connected people', oninput: (e) => { M.q = e.target.value; renderLive(); } })),
      h('div', { class: 'bar' },
        sure('muteall', 'Mute all', 'Tap again to mute all', () => muteAll(true), { class: 'grow' }),
        sure('unmuteall', 'Unmute all', 'Tap again to unmute all', () => muteAll(false), { class: 'grow ghost' })),
      h('div', { id: 'liveList' }));
  }
  const q = M.q.trim().toLowerCase();
  const shown = M.sessions.filter((s) => !q || [s.user, s.person, s.device].some((v) => (v || '').toLowerCase().includes(q)));
  // talking first, then by assignment
  shown.sort((a, b) => (b.sending - a.sending) || a.user.localeCompare(b.user));
  $('liveList').replaceChildren(...(shown.length ? shown.map(sessCard) : [h('div', { class: 'muted', style: { padding: '20px 4px' } }, q ? 'No match' : 'Nobody connected')]));
}

// ---------------- Users ----------------
function renderUsers() {
  if (M.edit) return renderEditor();
  const users = [...M.cfg.users].sort((a, b) => a.name.localeCompare(b.name));
  const online = new Set(M.sessions.map((s) => s.user));
  $('body').replaceChildren(
    h('div', { class: 'bar' }, h('button', { class: 'grow', onclick: () => { M.edit = { draft: { name: '', person: '', pin: '', admin: false, channels: [] }, isNew: true }; renderUsers(); } }, '+ Add user')),
    ...users.map((u) => h('button', { class: 'urow', onclick: () => { M.edit = { draft: structuredClone(u), isNew: false }; renderUsers(); } },
      h('span', { class: 'dot' + (online.has(u.name) ? ' ok' : '') }),
      h('div', { class: 'grow' }, h('b', {}, u.name), u.person ? h('div', { class: 'small muted' }, u.person) : null,
        h('div', { class: 'small muted' }, u.bridge ? 'NDI bridge' : `${u.channels.length} channel${u.channels.length === 1 ? '' : 's'}${u.admin ? ' · admin' : ''}${u.pin ? ' · PIN' : ''}`)),
      h('span', { class: 'muted' }, '›'))));
}

function renderEditor() {
  const { draft: u, isNew } = M.edit;
  const close = () => { M.edit = null; renderUsers(); };
  if (u.bridge) {
    $('body').replaceChildren(h('div', { class: 'panel' }, h('b', {}, u.name),
      h('p', { class: 'muted small' }, 'NDI bridge users are set up on the full admin page.'),
      h('div', { class: 'row' }, h('button', { onclick: close }, '‹ Back'), h('a', { href: 'admin.html', class: 'small' }, 'Full admin'))));
    return;
  }
  const access = (c) => { const a = u.channels.find((x) => x.ch === c.id); return a ? (a.talk ? 'T' : '') + (a.listen ? 'L' : '') : ''; };
  const setAccess = (c, val) => {
    const i = u.channels.findIndex((a) => a.ch === c.id);
    if (val === '') { if (i >= 0) u.channels.splice(i, 1); return; }
    const a = { ch: c.id, talk: val.includes('T'), listen: val.includes('L') };
    if (i >= 0) u.channels[i] = a; else u.channels.push(a);   // new channels go after the existing keys
  };
  const field = (label, key, attrs = {}) => [h('label', {}, label),
    h('input', { type: 'text', value: u[key] || '', oninput: (e) => { u[key] = attrs.trim ? e.target.value.trim() : e.target.value; }, ...attrs, trim: undefined })];
  const link = u.token ? `${(M.cfg.publicURL || location.origin).replace(/\/$/, '')}/#t=${u.token}` : '';

  $('body').replaceChildren(h('div', { class: 'panel' },
    h('div', { class: 'row' }, h('button', { class: 'ghost', onclick: close }, '‹ Back'), h('b', { class: 'grow' }, isNew ? 'New user' : u.name)),
    field('Assignment (e.g. Camera 1)', 'name', { autocapitalize: 'words' }),
    field('Person', 'person', { placeholder: 'e.g. John Doe', maxlength: 40, autocapitalize: 'words' }),
    field('PIN (optional)', 'pin', { inputmode: 'numeric', placeholder: 'none', trim: true }),
    h('label', { class: 'check' }, h('input', { type: 'checkbox', checked: !!u.admin, onchange: (e) => (u.admin = e.target.checked) }), 'Admin (can use this page with their PIN)'),
    h('label', {}, 'Channels'),
    h('div', {}, M.cfg.channels.map((c) => h('div', { class: 'acc' },
      h('span', { class: 'chip grow', style: { borderLeft: `3px solid ${c.color}` } }, c.name, c.listenOnly ? h('span', { class: 'muted' }, ' · listen-only') : ''),
      h('select', { 'aria-label': c.name + ' access', onchange: (e) => setAccess(c, e.target.value) },
        [['', 'No access'], ['L', 'Listen'], ['TL', 'Talk + Listen'], ['T', 'Talk only']].map(([v, t]) =>
          h('option', { value: v, selected: access(c) === v }, t)))))),
    h('div', { class: 'row', style: { marginTop: '14px' } },
      h('button', { class: 'grow', style: { background: '#2f3640' }, onclick: run(saveUser) }, isNew ? 'Add user' : 'Save'),
      h('button', { class: 'ghost', onclick: close }, 'Cancel')),
    link ? h('button', { style: { width: '100%', marginTop: '8px' }, onclick: run(() => shareInvite(u, link)) }, 'Share invite link') : null,
    !isNew ? sure('deluser', 'Delete user', 'Tap again — their devices and links stop working', deleteUser, { class: 'ghost', style: { width: '100%', marginTop: '8px', color: '#ffb4b6' } }) : null));
}

// Apply just this user's changes to the server's current config, so edits
// someone made meanwhile on the full admin page aren't overwritten.
async function saveUser() {
  const { draft: u, isNew } = M.edit;
  u.name = (u.name || '').trim();
  if (!u.name) throw new Error('Assignment name is required.');
  const cfg = await api('GET', 'config');
  if (isNew) {
    if (cfg.users.some((x) => x.name.toLowerCase() === u.name.toLowerCase())) throw new Error('There is already a user called ' + u.name + '.');
    cfg.users.push({ ...u, token: '' });
  } else {
    const i = cfg.users.findIndex((x) => x.id === u.id);
    if (i < 0) throw new Error('This user was deleted on another screen.');
    cfg.users[i] = { ...cfg.users[i], name: u.name, person: u.person, pin: u.pin, admin: u.admin, channels: u.channels };
  }
  M.cfg = await api('PUT', 'config', cfg);
  toast(isNew ? 'User added.' : 'Saved. Their devices were updated.', 'ok');
  M.edit = null;
  renderUsers();
}

async function deleteUser() {
  const { draft: u } = M.edit;
  const cfg = await api('GET', 'config');
  cfg.users = cfg.users.filter((x) => x.id !== u.id);
  M.cfg = await api('PUT', 'config', cfg);
  toast('User deleted.', 'ok');
  M.edit = null;
  renderUsers();
}

async function shareInvite(u, link) {
  if (navigator.share) {
    try { await navigator.share({ title: `OpenLine: ${u.name}`, text: `Your OpenLine intercom login (${u.name})`, url: link }); } catch { /* cancelled */ }
    return;
  }
  await navigator.clipboard.writeText(link);
  toast('Invite link copied.', 'ok');
}

// ---------------- plumbing ----------------
function render() {
  for (const b of document.querySelectorAll('[data-tab]')) b.classList.toggle('on', b.dataset.tab === M.tab);
  $('count').textContent = `${M.sessions.length} online`;
  if (M.tab === 'live') renderLive(); else renderUsers();
}

async function refresh() {
  M.sessions = await api('GET', 'sessions');
  $('count').textContent = `${M.sessions.length} online`;
  if (M.tab === 'live') renderLive();
}

async function start() {
  M.cfg = await api('GET', 'config');
  M.sessions = await api('GET', 'sessions');
  $('title').textContent = M.cfg.serverName || 'Admin';
  $('login').classList.add('hidden');
  $('m').classList.remove('hidden');
  render();
  setInterval(() => { if (!document.hidden) refresh().catch(() => {}); }, 2000);
}

for (const b of document.querySelectorAll('[data-tab]')) b.addEventListener('click', run(async () => {
  M.tab = b.dataset.tab; M.edit = null; $('body').replaceChildren();
  if (M.tab === 'users') M.cfg = await api('GET', 'config');
  render();
}));
$('loginForm').addEventListener('submit', run(async (e) => {
  e.preventDefault();
  const body = $('ap').value ? { password: $('ap').value } : { user: $('au').value.trim(), pin: $('apin').value };
  const r = await fetch('api/admin/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
  if (!r.ok) { $('loginErr').textContent = (await r.text()).trim(); return; }
  M.token = (await r.json()).token;
  sessionStorage.setItem('ol.admin', M.token);
  await start();
}));
if (M.token) start().catch(() => { sessionStorage.removeItem('ol.admin'); location.reload(); });
