// OpenLine admin page: live sessions, beltpack control, users, channels, server.
import { provision, bleSupported } from './ble.js';

const $ = (id) => document.getElementById(id);
// replaceChildren() would print null/false as text: drop them first.
const fill = (el, ...kids) => el.replaceChildren(...kids.flat().filter((k) => k !== null && k !== undefined && k !== false));
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

const A = { token: sessionStorage.getItem('ol.admin') || '', cfg: null, dirty: false, tab: 'live', sessions: [], devices: [], editUser: null, devCards: new Map(),
  show: (() => { try { return +localStorage.getItem('ol.admin.show') || 0; } catch { return 0; } })(), liveShow: 'all' };
function saveShowPref() { try { localStorage.setItem('ol.admin.show', String(A.show)); } catch { /* private mode */ } }

async function api(method, path, body) {
  const r = await fetch('api/' + path, {
    method, headers: { Authorization: 'Bearer ' + A.token, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (r.status === 401) { sessionStorage.removeItem('ol.admin'); location.reload(); throw new Error('unauthorized'); }
  if (!r.ok) throw new Error((await r.text()).trim() || r.statusText);
  return r.json();
}

function msg(text, kind = 'warn', ms = 4000) {
  const m = $('msg');
  m.textContent = text; m.className = 'banner ' + kind; m.classList.remove('hidden');
  clearTimeout(msg.t); if (ms) msg.t = setTimeout(() => m.classList.add('hidden'), ms);
}
const run = (f) => async (...a) => { try { await f(...a); } catch (e) { msg(e.message, 'bad', 6000); } };

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

function setDirty(d = true) {
  A.dirty = d;
  $('dirty').classList.toggle('hidden', !d);
  $('saveBtn').disabled = !d;
}

const chName = (id) => A.cfg.channels.find((c) => c.id === id)?.name || '#' + id;
// ---- shows ----
const multiShow = () => A.cfg.shows.length > 1;
const showName = (id) => A.cfg.shows.find((s) => s.id === id)?.name || '';
function curShow() { if (!A.cfg.shows.some((s) => s.id === A.show)) A.show = A.cfg.shows[0].id; return A.show; }
// Channel name, with its show when it belongs to a different show than `from`.
const chLabel = (id, from) => { const c = A.cfg.channels.find((x) => x.id === id); return c && multiShow() && c.show !== from ? `${showName(c.show)}: ${c.name}` : chName(id); };
// <option>s for picking a user, grouped by show when there are several.
const userOptions = (val, selected, label) => !multiShow()
  ? A.cfg.users.map((u) => h('option', { value: val(u), selected: selected(u) }, label(u)))
  : A.cfg.shows.map((s) => h('optgroup', { label: s.name }, A.cfg.users.filter((u) => u.show === s.id).map((u) => h('option', { value: val(u), selected: selected(u) }, label(u)))));
const chColor = (id) => A.cfg.channels.find((c) => c.id === id)?.color || '#666';
const baseURL = () => (A.cfg.publicURL || location.origin).replace(/\/$/, '');
const fmtAgo = (t) => { const s = Math.round((Date.now() - new Date(t)) / 1000); return s < 90 ? s + 's' : s < 5400 ? Math.round(s / 60) + 'm' : Math.round(s / 3600) + 'h'; };

// ---------------- Live ----------------
function remote(sid, op) { return api('POST', `sessions/${sid}/remote`, op).then(refresh); }

function renderLive() {
  const q = (A.liveFilter || '').trim().toLowerCase();
  const shown = A.sessions.filter((s) => (A.liveShow === 'all' || s.show === +A.liveShow) &&
    (!q || [s.user, s.person, s.device, s.remote, showName(s.show)].some((v) => (v || '').toLowerCase().includes(q))));
  const rows = shown.map((s) => h('tr', {},
    h('td', {}, h('b', {}, s.user), s.person ? h('div', {}, s.person) : null,
      h('div', { class: 'small muted' }, `${multiShow() ? showName(s.show) + ' · ' : ''}${s.client}${s.device ? ' · ' + s.device : ''}`)),
    h('td', { class: 'small muted' }, s.remote, h('br'), fmtAgo(s.connected)),
    h('td', {}, h('div', { class: 'row', style: { flexWrap: 'wrap', gap: '4px' } }, s.channels.map((id) => {
      const t = s.talk.includes(id), l = s.listen.includes(id);
      return h('span', { class: 'chip', style: { borderLeft: `3px solid ${chColor(id)}` } }, chLabel(id, s.show), ' ',
        h('button', { class: t ? 'on' : 'ghost', style: { minHeight: '24px', color: t ? '#ffb4b6' : '' }, title: 'Force talk', onclick: run(() => remote(s.sid, { op: 'talk', ch: id, on: !t })) }, 'T'),
        h('button', { class: l ? 'on' : 'ghost', style: { minHeight: '24px' }, title: 'Force listen', onclick: run(() => remote(s.sid, { op: 'listen', ch: id, on: !l })) }, 'L'));
    }))),
    h('td', {}, s.sending ? h('span', { class: 'pill bad' }, 'TALKING') : s.talk.length ? h('span', { class: 'pill warn' }, 'latched') : '',
      s.tally === 'program' ? h('span', { class: 'pill bad', style: { marginLeft: '4px' } }, 'ON AIR') : s.tally === 'preview' ? h('span', { class: 'pill ok', style: { marginLeft: '4px' } }, 'PREVIEW') : ''),
    h('td', {}, h('div', { class: 'row' },
      h('button', { class: s.muted ? 'on' : '', onclick: run(() => remote(s.sid, { op: 'mute', on: !s.muted })) }, s.muted ? 'Unmute mic' : 'Mute mic'),
      s.client === 'web' ? h('button', { class: s.tallyView ? 'on' : '', title: 'Switch this phone to / from the full-screen tally view',
        onclick: run(() => remote(s.sid, { op: 'tallyscreen', on: !s.tallyView })) }, s.tallyView ? 'Tally screen: on' : 'Tally screen: off') : null,
      h('button', { onclick: run(() => remote(s.sid, { op: 'identify' })) }, 'Identify'),
      sure('kick' + s.sid, 'Kick', 'Tap again to kick', () => remote(s.sid, { op: 'kick' })))),
  ));
  // The search box is built once so typing isn't interrupted by the 2 s refresh.
  const box = $('tab-live');
  if (!box.querySelector('#liveFilter')) {
    fill(box, 
      h('div', { class: 'row', style: { marginBottom: '8px', flexWrap: 'wrap' } },
        h('input', { id: 'liveFilter', type: 'search', placeholder: 'Search name, assignment, device or IP…', 'aria-label': 'Search connected users',
          style: { maxWidth: '360px' }, oninput: (e) => { A.liveFilter = e.target.value; renderLive(); } }),
        multiShow() ? h('select', { 'aria-label': 'Show', style: { width: 'auto' }, onchange: (e) => { A.liveShow = e.target.value; renderLive(); } },
          h('option', { value: 'all', selected: A.liveShow === 'all' }, 'All shows'),
          A.cfg.shows.map((x) => h('option', { value: x.id, selected: String(A.liveShow) === String(x.id) }, x.name))) : null,
        h('span', { id: 'liveCount', class: 'small muted' })),
      h('div', { id: 'liveTable' }));
  }
  $('liveCount').textContent = `${shown.length === A.sessions.length ? A.sessions.length : shown.length + ' of ' + A.sessions.length} connected · T/L force talk/listen per channel`;
  fill($('liveTable'), h('div', { class: 'tablebox' }, h('table', {},
    h('thead', {}, h('tr', {}, ['Assignment / person', 'From', 'Channels', 'State', 'Control'].map((t) => h('th', {}, t)))),
    h('tbody', {}, rows.length ? rows : h('tr', {}, h('td', { colspan: 5, class: 'muted' }, q ? 'No match' : 'Nobody connected'))))));
}

// ---------------- Beltpacks ----------------
function battPill(t) {
  if (!t) return h('span', { class: 'pill' }, 'no data');
  if (t.bat < 0) return h('span', { class: 'pill ok' }, t.poe ? 'PoE' : 'mains');
  const cls = t.bat <= 15 ? 'bad' : t.bat <= 35 ? 'warn' : 'ok';
  return h('span', { class: 'pill batt ' + cls }, `${t.bat}%${t.chg ? ' ⚡' : ''}`);
}

function devStats(d) {
  const t = d.telemetry;
  return [
    d.online ? h('span', { class: 'pill ok' }, 'online') : h('span', { class: 'pill' }, 'offline'),
    battPill(t),
    t?.net ? h('span', { class: 'pill' }, t.net.toUpperCase() + (t.poe ? ' + PoE' : '')) : null,
    t?.ip ? h('span', { class: 'pill' }, t.ip) : null,
    t?.rssi ? h('span', { class: 'pill ' + (t.rssi < -75 ? 'warn' : '') }, `${t.rssi} dBm`) : null,
    t ? h('span', { class: 'pill ' + (t.hs ? '' : 'warn') }, t.hs ? 'headset' : 'no headset') : null,
    t?.fw ? h('span', { class: 'pill' }, 'fw ' + t.fw) : null,
    t?.up ? h('span', { class: 'pill' }, 'up ' + fmtAgo(Date.now() - t.up * 1000)) : null,
    d.session?.person ? h('span', { class: 'pill' }, d.session.person) : null,
    d.session?.muted ? h('span', { class: 'pill bad' }, 'MIC MUTED') : null,
  ];
}

function devKeys(d) {
  const s = d.session;
  if (!s || !s.channels.length) return h('div', { class: 'small muted' }, d.online ? 'No channels — assign a user.' : 'Offline');
  return h('div', { class: 'keys' }, s.channels.slice(0, 4).map((id, i) => {
    const t = s.talk.includes(id), l = s.listen.includes(id);
    const vol = h('input', { type: 'number', min: 0, max: 100, value: 75, 'aria-label': 'Volume', style: { minHeight: '28px', padding: '2px 4px' } });
    return h('div', { class: 'key', style: { '--c': chColor(id) } },
      h('span', { class: 'kn' }, `${i + 1} ${chName(id)}`),
      h('button', { class: 'talk' + (t ? ' on' : ''), onclick: run(() => remote(s.sid, { op: 'talk', ch: id, on: !t })) }, t ? 'TALKING' : 'talk off'),
      h('button', { class: l ? 'on' : 'ghost', onclick: run(() => remote(s.sid, { op: 'listen', ch: id, on: !l })) }, l ? 'listening' : 'muted'),
      h('div', { class: 'row', style: { gap: '4px' } }, vol, h('button', { onclick: run(() => remote(s.sid, { op: 'vol', ch: id, v: +vol.value })) }, 'Set')));
  }));
}

function buildDevCard(d) {
  const userSel = h('select', {}, h('option', { value: '' }, '— unassigned —'),
    userOptions((u) => u.id, (u) => u.id === d.userId, (u) => u.name + (u.bridge ? ' (bridge)' : '')));
  const name = h('input', { type: 'text', value: d.name });
  const au = { ...d.audio };
  const slider = (key, label) => h('div', {}, h('label', {}, label, ' ', h('span', { class: 'muted' }, String(au[key]))),
    h('input', { type: 'range', min: 0, max: 100, value: au[key], oninput: (e) => { au[key] = +e.target.value; e.target.previousSibling.lastChild.textContent = e.target.value; } }));
  const micType = h('select', { onchange: (e) => (au.micType = e.target.value) },
    h('option', { value: 'dynamic', selected: au.micType === 'dynamic' }, 'Dynamic (most intercom headsets)'),
    h('option', { value: 'electret', selected: au.micType === 'electret' }, 'Electret (mic bias on)'));

  const n = { ...d.net };
  const f = {};
  const inp = (k, label, ph = '') => h('div', { class: 'grow' }, h('label', {}, label), f[k] = h('input', { type: 'text', value: n[k] || '', placeholder: ph }));
  f.mode = h('select', {}, ['wifi', 'ethernet', 'ble'].map((m) => h('option', { value: m, selected: n.mode === m }, { wifi: 'Wi‑Fi', ethernet: 'Ethernet / PoE', ble: 'Bluetooth to phone' }[m])));
  f.dhcp = h('select', {}, h('option', { value: '1', selected: n.dhcp }, 'DHCP'), h('option', { value: '0', selected: !n.dhcp }, 'Static IP'));
  f.pass = h('input', { type: 'password', placeholder: '(unchanged)' });
  const staticBox = h('div', { class: n.dhcp ? 'hidden' : '' },
    h('div', { class: 'row' }, inp('ip', 'IP address', '10.0.0.50'), inp('mask', 'Subnet mask', '255.255.255.0')),
    h('div', { class: 'row' }, inp('gateway', 'Gateway', '10.0.0.1'), inp('dns', 'DNS', '1.1.1.1')));
  f.dhcp.addEventListener('change', () => staticBox.classList.toggle('hidden', f.dhcp.value === '1'));

  const stats = h('div', { class: 'stats' }, devStats(d));
  const keys = h('div', {}, devKeys(d));
  const ctl = h('div', { class: 'row', style: { flexWrap: 'wrap' } });
  const card = h('div', { class: 'dev' },
    h('div', { class: 'row' }, h('b', { class: 'grow' }, d.name), h('span', { class: 'small muted' }, d.id.toUpperCase())),
    stats, keys, ctl,
    h('div', { class: 'row' }, h('div', { class: 'grow' }, h('label', {}, 'Name'), name), h('div', { class: 'grow' }, h('label', {}, 'Assigned user'), userSel)),
    h('details', {}, h('summary', {}, 'Audio & display'),
      h('label', {}, 'Mic type'), micType,
      slider('micGain', 'Mic gain'), slider('sidetone', 'Sidetone'), slider('hpLevel', 'Headphone level'), slider('bright', 'Screen / LED brightness')),
    h('button', { onclick: run(async () => {
      await api('PUT', `devices/${d.id}`, { name: name.value, userId: userSel.value, audio: au });
      msg(`Saved ${name.value}` + (d.online ? ' — pushed to beltpack' : ''), 'warn');
      A.devCards.clear(); await refresh();
    }) }, 'Save name / user / audio'),
    h('details', {}, h('summary', {}, 'Network'),
      h('div', { class: 'row' }, h('div', { class: 'grow' }, h('label', {}, 'Connection'), f.mode), h('div', { class: 'grow' }, h('label', {}, 'Addressing'), f.dhcp)),
      staticBox,
      h('div', { class: 'row' }, inp('ssid', 'Wi‑Fi network (SSID)'), h('div', { class: 'grow' }, h('label', {}, 'Wi‑Fi password'), f.pass)),
      inp('server', 'Server URL', baseURL()),
      sure('net' + d.id, 'Apply network & reboot', d.online ? 'Tap again: apply & reboot' : 'Tap again (it is offline)', async () => {
        const body = { mode: f.mode.value, dhcp: f.dhcp.value === '1', ip: f.ip.value.trim(), mask: f.mask.value.trim(), gateway: f.gateway.value.trim(), dns: f.dns.value.trim(), ssid: f.ssid.value, pass: f.pass.value, server: f.server.value.trim() };
        await api('PUT', `devices/${d.id}/net`, body);
        msg(d.online ? 'Network settings sent — beltpack is rebooting' : 'Saved (beltpack offline; settings are sent when you apply while it is online)');
      }, { style: { marginTop: '8px' } })),
    sure('forget' + d.id, 'Forget device', 'Tap again to forget', async () => { await api('DELETE', `devices/${d.id}`); A.devCards.clear(); await refresh(); }, { class: 'ghost' }),
  );
  return { card, stats, keys, ctl };
}

function updateDevCard(c, d) {
  c.stats.replaceChildren(...devStats(d).filter(Boolean));
  c.keys.replaceChildren(devKeys(d));
  const s = d.session;
  c.ctl.replaceChildren(...(s ? [
    h('button', { class: s.muted ? 'on' : '', onclick: run(() => remote(s.sid, { op: 'mute', on: !s.muted })) }, s.muted ? 'Unmute mic' : 'Mute mic'),
    h('button', { onclick: run(() => remote(s.sid, { op: 'identify' })) }, 'Identify (flash)'),
    sure('reboot' + s.sid, 'Reboot', 'Tap again to reboot', () => remote(s.sid, { op: 'reboot' })),
  ] : []));
}

function renderDevices() {
  const box = $('tab-devices');
  const ids = A.devices.map((d) => d.id).join();
  if (box.dataset.ids !== ids || !box.firstChild) {
    box.dataset.ids = ids;
    A.devCards.clear();
    const grid = h('div', { class: 'devgrid' });
    for (const d of A.devices) { const c = buildDevCard(d); A.devCards.set(d.id, c); grid.append(c.card); }
    fill(box, 
      h('div', { class: 'panel' },
        h('div', { class: 'row', style: { flexWrap: 'wrap' } },
          h('div', { class: 'grow small muted' }, 'New beltpacks appear here as soon as they connect. Set one up over Bluetooth from this computer (Chrome/Edge), or provision it with only Wi‑Fi + server URL and assign a user here.'),
          h('button', { onclick: openProvision, disabled: !bleSupported() }, 'Set up beltpack via Bluetooth'))),
      A.devices.length ? grid : h('div', { class: 'muted' }, 'No beltpacks yet.'));
  }
  for (const d of A.devices) { const c = A.devCards.get(d.id); if (c) updateDevCard(c, d); }
}

function openProvision() {
  const f = {};
  const field = (k, label, attrs = {}) => h('div', { class: 'grow' }, h('label', {}, label), f[k] = h('input', { type: 'text', ...attrs }));
  f.mode = h('select', {}, h('option', { value: 'wifi' }, 'Wi‑Fi'), h('option', { value: 'ethernet' }, 'Ethernet / PoE'), h('option', { value: 'ble' }, 'Bluetooth to phone app'));
  f.user = h('select', {}, h('option', { value: '' }, '— assign later in admin —'), userOptions((u) => u.token, () => false, (u) => u.name));
  f.dhcp = h('select', {}, h('option', { value: '1' }, 'DHCP'), h('option', { value: '0' }, 'Static IP'));
  const status = h('div', { class: 'small muted' });
  const panel = h('div', { class: 'panel' },
    h('b', {}, 'Set up beltpack via Bluetooth'),
    h('div', { class: 'row' }, h('div', { class: 'grow' }, h('label', {}, 'Mode'), f.mode), h('div', { class: 'grow' }, h('label', {}, 'User'), f.user)),
    h('div', { class: 'row' }, field('ssid', 'Wi‑Fi SSID'), field('pass', 'Wi‑Fi password', { type: 'password' })),
    field('server', 'Server URL', { value: baseURL() }),
    h('div', { class: 'row' }, h('div', { class: 'grow' }, h('label', {}, 'Addressing'), f.dhcp), field('ip', 'Static IP'), field('mask', 'Mask', { value: '255.255.255.0' }), field('gw', 'Gateway')),
    h('div', { class: 'row', style: { marginTop: '10px' } },
      h('button', { onclick: run(async () => {
        const cfg = { mode: f.mode.value, server: f.server.value.trim(), dhcp: f.dhcp.value === '1' ? 1 : 0 };
        if (f.user.value) cfg.token = f.user.value;
        if (cfg.mode === 'wifi') { cfg.ssid = f.ssid.value; cfg.pass = f.pass.value; }
        if (!cfg.dhcp) Object.assign(cfg, { ip: f.ip.value.trim(), mask: f.mask.value.trim(), gw: f.gw.value.trim() });
        const info = await provision(cfg, (s) => (status.textContent = s));
        status.textContent = `Done${info.id ? ' — device ' + info.id : ''}. It will reboot and appear in this list.`;
      }) }, 'Choose beltpack & send'),
      h('button', { class: 'ghost', onclick: () => panel.remove() }, 'Cancel')),
    status);
  $('tab-devices').prepend(panel);
}

// ---------------- Users ----------------
const parseTally = (v) => [...new Set(v.split(/[ ,]+/).map((x) => parseInt(x, 10)).filter((n) => n > 0))];
const accessLabel = (a) => a.talk && a.listen ? 'Talk+Listen' : a.talk ? 'Talk' : a.listen ? 'Listen' : '';

function renderUsers() {
  const show = curShow();
  const users = A.cfg.users.filter((u) => u.show === show);
  const table = h('div', { class: 'tablebox' }, h('table', {},
    h('thead', {}, h('tr', {}, ['Assignment', 'Person', 'PIN', 'vMix tally', 'Role', 'Channels (key order)', ''].map((t) => h('th', {}, t)))),
    h('tbody', {}, users.length ? users.map((u) => h('tr', {},
      h('td', {}, h('input', { type: 'text', value: u.name, oninput: (e) => { u.name = e.target.value; setDirty(); } })),
      h('td', {}, h('input', { type: 'text', value: u.person || '', placeholder: 'e.g. John Doe', maxlength: 40, 'aria-label': u.name + ' person name',
        oninput: (e) => { u.person = e.target.value; setDirty(); } })),
      h('td', { style: { width: '90px' } }, h('input', { type: 'text', value: u.pin || '', placeholder: 'none', inputmode: 'numeric', oninput: (e) => { u.pin = e.target.value.trim(); setDirty(); } })),
      h('td', { style: { width: '90px' } }, h('input', { type: 'text', value: (u.tally || []).join(', '), placeholder: 'none', inputmode: 'numeric',
        title: 'vMix input number(s) this person is on, e.g. 2 or 2, 5', 'aria-label': u.name + ' vMix tally input',
        oninput: (e) => { u.tally = parseTally(e.target.value); setDirty(); } })),
      h('td', { class: 'small' },
        h('label', { style: { margin: 0, color: 'inherit' } }, h('input', { type: 'checkbox', checked: !!u.admin, onchange: (e) => { u.admin = e.target.checked; setDirty(); } }), ' admin'),
        h('label', { style: { margin: 0, color: 'inherit' } }, h('input', { type: 'checkbox', checked: !!u.bridge, onchange: (e) => { u.bridge = e.target.checked; setDirty(); } }), ' NDI bridge'),
        h('label', { style: { margin: 0, color: 'inherit' }, title: 'The bridge login that plays channels out at the venue (NDI / sound card). It hears every channel that has an Output set on the Channels tab.' },
          h('input', { type: 'checkbox', checked: !!u.outputs, onchange: (e) => { u.outputs = e.target.checked; if (u.outputs) u.bridge = true; setDirty(); renderUsers(); } }), ' channel outputs'),
        h('label', { style: { margin: 0, color: 'inherit' }, title: 'Open this user\'s app in the full-screen tally view' },
          h('input', { type: 'checkbox', checked: !!u.tallyScreen, onchange: (e) => { u.tallyScreen = e.target.checked; setDirty(); } }), ' tally screen')),
      h('td', { class: 'small' }, u.outputs ? h('span', { class: 'muted' }, 'every channel with an Output')
        : u.channels.map((a, i) => h('span', { class: 'chip', style: { borderLeft: `3px solid ${chColor(a.ch)}`, marginRight: '4px' } }, `${i + 1} ${chLabel(a.ch, u.show)} · ${accessLabel(a)}`))),
      h('td', {}, h('div', { class: 'row' },
        h('button', { onclick: () => { A.editUser = u; renderUsers(); } }, 'Edit'),
        h('button', { onclick: () => { A.editUser = u; A.showInvite = true; renderUsers(); } }, 'Invite'))),
    )) : h('tr', {}, h('td', { colspan: 7, class: 'muted' }, 'No users in this show yet.')))));
  fill($('tab-users'), 
    h('div', { class: 'row', style: { marginBottom: '8px' } },
      h('div', { class: 'grow small muted' }, (multiShow() ? `Users in ${showName(show)}. ` : '') + 'Each user is a login for an assignment (e.g. Camera 1), a beltpack role, or an NDI bridge link. Person = who normally has it; a device can enter its own name, which wins for that device. vMix tally = the vMix input number(s) that person is on. Channel order = beltpack key order.'),
      h('button', { onclick: () => { const u = { name: 'New user ' + (users.length + 1), pin: '', channels: [], token: '', show }; A.cfg.users.push(u); A.editUser = u; setDirty(); renderUsers(); } }, 'Add user')),
    table, A.editUser && A.cfg.users.includes(A.editUser) && A.editUser.show === show ? userEditor(A.editUser) : null);
}

function userEditor(u) {
  if (u.outputs) {
    return h('div', { class: 'panel', style: { marginTop: '12px' } },
      h('div', { class: 'row' }, h('b', { class: 'grow' }, 'Edit ', u.name), h('button', { class: 'ghost', onclick: () => { A.editUser = null; renderUsers(); } }, 'Close')),
      h('p', { class: 'small muted' }, 'Channel outputs login: it hears every channel that has an Output set on the Channels tab, in every show. Put its token in the "outputs" section of the bridge\'s bridge.json.'),
      u.token ? h('div', {}, h('label', {}, 'Bridge token (outputs → token in bridge.json)'), h('input', { type: 'text', readOnly: true, value: u.token, onclick: (e) => e.target.select() }))
        : h('div', { class: 'small muted' }, 'Save to generate this user\'s token.'),
      h('div', { class: 'row', style: { marginTop: '10px' } },
        h('button', { onclick: () => { u.token = ''; setDirty(); msg('New token is generated when you save.'); } }, 'Regenerate token'),
        sure('deluser', 'Delete user', 'Tap again to delete', () => { A.cfg.users.splice(A.cfg.users.indexOf(u), 1); A.editUser = null; setDirty(); renderUsers(); msg(`Deleted ${u.name}. Press Save changes to apply.`); }, { class: 'ghost' })));
  }
  const all = A.cfg.channels.map((c) => {
    const i = u.channels.findIndex((a) => a.ch === c.id);
    return { c, a: i >= 0 ? u.channels[i] : null, i };
  }).sort((x, y) => (x.i < 0 ? 999 : x.i) - (y.i < 0 ? 999 : y.i));
  // This user's show (and anything already assigned) first; other shows tucked away.
  const rows = all.filter((r) => r.a || r.c.show === u.show);
  const others = all.filter((r) => !r.a && r.c.show !== u.show);
  const move = (ch, d) => {
    const i = u.channels.findIndex((a) => a.ch === ch), j = i + d;
    if (i < 0 || j < 0 || j >= u.channels.length) return;
    [u.channels[i], u.channels[j]] = [u.channels[j], u.channels[i]];
    setDirty(); renderUsers();
  };
  const set = (c, val) => {
    const i = u.channels.findIndex((a) => a.ch === c.id);
    if (val === '') { if (i >= 0) u.channels.splice(i, 1); }
    else {
      const a = { ch: c.id, talk: val.includes('T'), listen: val.includes('L') };
      if (i >= 0) u.channels[i] = a; else u.channels.push(a);
    }
    setDirty(); renderUsers();
  };
  const chTable = (list) => h('div', { class: 'tablebox', style: { marginTop: '8px' } }, h('table', {}, h('tbody', {}, list.map(({ c, a }) => h('tr', {},
    h('td', { style: { width: '40px' } }, a ? String(u.channels.indexOf(a) + 1) : ''),
    h('td', {}, h('span', { class: 'chip', style: { borderLeft: `3px solid ${c.color}` } }, chLabel(c.id, u.show)), c.listenOnly ? h('span', { class: 'small muted' }, ' listen-only') : ''),
    h('td', {}, h('select', { onchange: (e) => set(c, e.target.value) },
      [['', 'No access'], ['L', 'Listen'], ['TL', 'Talk + Listen'], ['T', 'Talk only']].map(([v, t]) =>
        h('option', { value: v, selected: (a ? (a.talk ? 'T' : '') + (a.listen ? 'L' : '') : '') === v }, t)))),
    h('td', {}, a ? h('div', { class: 'row' }, h('button', { onclick: () => move(c.id, -1) }, '↑'), h('button', { onclick: () => move(c.id, 1) }, '↓')) : ''),
  )))));
  const link = u.token ? `${baseURL()}/#t=${u.token}` : '';
  return h('div', { class: 'panel', style: { marginTop: '12px' } },
    h('div', { class: 'row' }, h('b', { class: 'grow' }, 'Edit ', u.name), h('button', { class: 'ghost', onclick: () => { A.editUser = null; A.showInvite = false; renderUsers(); } }, 'Close')),
    multiShow() ? h('div', { class: 'row', style: { marginTop: '8px' } },
      h('label', { style: { margin: 0 } }, 'Show'),
      h('select', { style: { width: 'auto' }, onchange: (e) => { u.show = +e.target.value; A.show = u.show; saveShowPref(); setDirty(); renderTab(); } },
        A.cfg.shows.map((s) => h('option', { value: s.id, selected: s.id === u.show }, s.name)))) : null,
    chTable(rows),
    others.length ? h('details', { style: { marginTop: '8px' } }, h('summary', { class: 'small' }, `Channels from other shows (${others.length})`), chTable(others)) : null,
    h('label', {}, 'vMix tally inputs (input numbers this user is, e.g. camera on input 2 → "2")'),
    h('input', { type: 'text', inputmode: 'numeric', placeholder: 'none', value: (u.tally || []).join(', '),
      oninput: (e) => { u.tally = parseTally(e.target.value); setDirty(); } }),
    h('div', { class: 'row', style: { marginTop: '10px', flexWrap: 'wrap' } },
      h('button', { onclick: () => { u.token = ''; setDirty(); msg('New token is generated when you save. Old invite links and devices using it stop working.'); } }, 'Regenerate token'),
      sure('deluser', 'Delete user', 'Tap again to delete', () => { A.cfg.users.splice(A.cfg.users.indexOf(u), 1); A.editUser = null; setDirty(); renderUsers(); msg(`Deleted ${u.name}. Press Save changes to apply.`); }, { class: 'ghost' })),
    link ? h('div', { style: { marginTop: '12px' } },
      h('label', {}, u.bridge ? 'Bridge token (put in bridge.json)' : 'Invite link — opens the intercom already signed in'),
      h('input', { type: 'text', readOnly: true, value: u.bridge ? u.token : link, onclick: (e) => e.target.select() }),
      !u.bridge && A.showInvite ? h('img', { class: 'qr', style: { marginTop: '8px' }, alt: 'QR code for invite link', src: `api/qr?k=${A.token}&text=${encodeURIComponent(link)}` }) : null)
      : h('div', { class: 'small muted', style: { marginTop: '8px' } }, 'Save to generate this user\'s invite link / token.'));
}

// ---------------- Channels ----------------
function setOut(c, patch) {
  c.out = { ...(c.out || {}), ...patch };
  if (!c.out.ndi && !c.out.card) delete c.out;
  setDirty();
}

function renderChannels() {
  const show = curShow();
  const all = A.cfg.channels;
  const chs = all.filter((c) => c.show === show);
  // Reorder within this show (swap the two channels' places in the full list).
  const move = (c, d) => {
    const i = chs.indexOf(c), j = i + d;
    if (j < 0 || j >= chs.length) return;
    const a = all.indexOf(chs[i]), b = all.indexOf(chs[j]);
    [all[a], all[b]] = [all[b], all[a]];
    setDirty(); renderChannels();
  };
  const usedCards = new Map();
  for (const c of all) if (c.out?.card) usedCards.set(c.out.card, [...(usedCards.get(c.out.card) || []), c]);
  const anyOut = all.some((c) => c.out);
  const hasOutputsUser = A.cfg.users.some((u) => u.outputs);
  fill($('tab-channels'), 
    h('div', { class: 'row', style: { marginBottom: '8px' } },
      h('div', { class: 'grow small muted' }, (multiShow() ? `Channels in ${showName(show)}. ` : '') +
        'Listen-only channels (program, NDI feeds) can only be talked on by NDI bridge users. Program channels are dipped 12 dB while anyone talks. ' +
        'Output plays a channel at the venue through the bridge: as its own NDI source and/or on a sound card output.'),
      h('button', { onclick: () => { const id = Math.max(0, ...all.map((c) => c.id)) + 1; all.push({ id, name: 'Channel ' + (chs.length + 1), color: '#4a90d9', show }); setDirty(); renderChannels(); } }, 'Add channel')),
    anyOut && !hasOutputsUser ? h('div', { class: 'banner warn', style: { borderRadius: '8px', marginBottom: '8px' } },
      'Outputs need a bridge login with the "channel outputs" role (Users tab), and its token in the "outputs" section of bridge.json.') : null,
    h('div', { class: 'tablebox' }, h('table', {},
      h('thead', {}, h('tr', {}, ['#', 'Name', 'Color', 'Listen-only', 'Program (duck)', 'Output', ''].map((t) => h('th', {}, t)))),
      h('tbody', {}, chs.length ? chs.map((c, n) => {
        const clash = c.out?.card && usedCards.get(c.out.card).length > 1;
        return h('tr', {},
          // position in this show's list; the internal id (used in links and bridge configs) never changes
          h('td', { class: 'muted', title: `Channel id ${c.id}` }, String(n + 1)),
          h('td', {}, h('input', { type: 'text', value: c.name, oninput: (e) => { c.name = e.target.value; setDirty(); } })),
          h('td', {}, h('input', { type: 'color', class: 'swatch', value: c.color, oninput: (e) => { c.color = e.target.value; setDirty(); } })),
          h('td', {}, h('input', { type: 'checkbox', checked: !!c.listenOnly, onchange: (e) => { c.listenOnly = e.target.checked; setDirty(); } })),
          h('td', {}, h('input', { type: 'checkbox', checked: !!c.program, onchange: (e) => { c.program = e.target.checked; setDirty(); } })),
          h('td', {}, h('div', { class: 'row', style: { gap: '8px', flexWrap: 'nowrap' } },
            h('label', { class: 'small', style: { margin: 0, color: 'inherit', display: 'flex', gap: '4px', alignItems: 'center', whiteSpace: 'nowrap' }, title: 'Publish this channel as its own NDI source' },
              h('input', { type: 'checkbox', checked: !!c.out?.ndi, onchange: (e) => setOut(c, { ndi: e.target.checked }) }), 'NDI'),
            h('select', { style: { width: '104px', minHeight: '32px', padding: '2px 6px' }, 'aria-label': c.name + ' sound card output',
              title: clash ? 'More than one channel uses this card output; they are mixed together' : 'Sound card output channel on the bridge PC',
              onchange: (e) => { setOut(c, { card: +e.target.value }); renderChannels(); } },
              Array.from({ length: 65 }, (_, n) => h('option', { value: n, selected: (c.out?.card || 0) === n }, n ? 'Card out ' + n : 'No card'))),
            clash ? h('span', { class: 'pill warn', title: 'Shared with another channel' }, 'shared') : null)),
          h('td', {}, h('div', { class: 'row' },
            h('button', { onclick: () => move(c, -1) }, '↑'), h('button', { onclick: () => move(c, 1) }, '↓'),
            sure('delch' + c.id, 'Delete', 'Tap again to delete', () => { all.splice(all.indexOf(c), 1); setDirty(); renderChannels(); msg(`Deleted ${c.name} (users lose access to it). Press Save changes to apply.`); }, { class: 'ghost' }))));
      }) : h('tr', {}, h('td', { colspan: 7, class: 'muted' }, 'No channels in this show yet.'))))));
}

// ---------------- Shows ----------------
function uniqueShowName(base) {
  const names = new Set(A.cfg.shows.map((s) => s.name.toLowerCase()));
  let n = base, i = 2;
  while (names.has(n.toLowerCase())) n = `${base} ${i++}`;
  return n;
}

function duplicateShow(src) {
  const id = Math.max(0, ...A.cfg.shows.map((s) => s.id)) + 1;
  A.cfg.shows.push({ id, name: uniqueShowName(src.name + ' copy') });
  let next = Math.max(0, ...A.cfg.channels.map((c) => c.id)) + 1;
  const map = new Map();
  for (const c of A.cfg.channels.filter((c) => c.show === src.id)) {
    const nc = { ...structuredClone(c), id: next++, show: id };
    delete nc.out; // outputs are per channel: pick new ones for the copy
    map.set(c.id, nc.id);
    A.cfg.channels.push(nc);
  }
  for (const u of A.cfg.users.filter((u) => u.show === src.id && !u.outputs)) {
    const nu = structuredClone(u);
    delete nu.id;
    Object.assign(nu, { show: id, token: '', pin: '', channels: u.channels.map((a) => ({ ...a, ch: map.get(a.ch) ?? a.ch })) });
    A.cfg.users.push(nu);
  }
  A.show = id; saveShowPref(); setDirty(); renderTab();
  msg(`Copied ${src.name}: ${map.size} channels and its users, with new invite links (PINs aren't copied). Press Save changes to apply.`, 'warn', 8000);
}

function deleteShow(s) {
  const gone = new Set(A.cfg.channels.filter((c) => c.show === s.id).map((c) => c.id));
  A.cfg.channels = A.cfg.channels.filter((c) => !gone.has(c.id));
  A.cfg.users = A.cfg.users.filter((u) => u.show !== s.id);
  for (const u of A.cfg.users) u.channels = u.channels.filter((a) => !gone.has(a.ch));
  A.cfg.shows.splice(A.cfg.shows.indexOf(s), 1);
  if (A.show === s.id) { A.show = A.cfg.shows[0].id; saveShowPref(); }
  A.editUser = null; setDirty(); renderTab();
  msg(`Deleted ${s.name}. Press Save changes to apply.`);
}

function renderShows() {
  const shows = A.cfg.shows;
  fill($('tab-shows'), 
    h('div', { class: 'row', style: { marginBottom: '8px' } },
      h('div', { class: 'grow small muted' }, 'A show is its own set of channels and users, for running another show at the same time without crossing over. Assignment names only need to be unique within a show. Pick the show you are editing at the top of the page. Duplicate copies a show\'s channels and users with new invite links (PINs aren\'t copied).'),
      h('button', { onclick: () => { const id = Math.max(0, ...shows.map((s) => s.id)) + 1; shows.push({ id, name: uniqueShowName('Show ' + (shows.length + 1)) }); A.show = id; saveShowPref(); setDirty(); renderTab(); } }, 'Add show')),
    h('div', { class: 'tablebox' }, h('table', {},
      h('thead', {}, h('tr', {}, ['Name', 'Channels', 'Users', 'Online', ''].map((t) => h('th', {}, t)))),
      h('tbody', {}, shows.map((s) => {
        const nch = A.cfg.channels.filter((c) => c.show === s.id).length;
        const nu = A.cfg.users.filter((u) => u.show === s.id).length;
        const on = A.sessions.filter((x) => x.show === s.id).length;
        return h('tr', {},
          h('td', {}, h('input', { type: 'text', value: s.name, 'aria-label': 'Show name', oninput: (e) => { s.name = e.target.value; setDirty(); renderShowPicker(); } })),
          h('td', {}, String(nch)), h('td', {}, String(nu)), h('td', {}, String(on)),
          h('td', {}, h('div', { class: 'row' },
            h('button', { class: A.show === s.id ? 'on' : '', onclick: () => { A.show = s.id; saveShowPref(); A.tab = 'channels'; renderTab(); } }, 'Edit'),
            h('button', { onclick: () => duplicateShow(s) }, 'Duplicate'),
            shows.length > 1 ? sure('delshow' + s.id, 'Delete', `Tap again: deletes ${nch} channels, ${nu} users`, () => deleteShow(s), { class: 'ghost' }) : null)));
      })))));
}

function renderShowPicker() {
  const sel = $('showSel');
  sel.classList.toggle('hidden', !multiShow());
  sel.replaceChildren(...A.cfg.shows.map((s) => h('option', { value: s.id, selected: s.id === curShow() }, s.name)));
}

// ---------------- Server ----------------
function renderServer() {
  const c = A.cfg;
  fill($('tab-server'), h('div', { class: 'panel', style: { maxWidth: '560px' } },
    h('label', {}, 'Server name'), h('input', { type: 'text', value: c.serverName, oninput: (e) => { c.serverName = e.target.value; setDirty(); } }),
    h('label', {}, 'Public URL (how off-site people reach this server; used in invite links)'),
    h('input', { type: 'text', value: c.publicURL || '', placeholder: 'https://intercom.example.com', oninput: (e) => { c.publicURL = e.target.value; setDirty(); } }),
    h('label', {}, 'New admin password (leave empty to keep)'),
    h('input', { type: 'password', autocomplete: 'new-password', oninput: (e) => { A.newPassword = e.target.value; setDirty(); } }),
  ));
}

// ---------------- plumbing ----------------
function renderTab() {
  for (const b of document.querySelectorAll('[data-tab]')) b.classList.toggle('on', b.dataset.tab === A.tab);
  for (const t of ['live', 'devices', 'users', 'channels', 'shows', 'server']) $('tab-' + t).classList.toggle('hidden', t !== A.tab);
  renderShowPicker();
  ({ live: renderLive, devices: renderDevices, users: renderUsers, channels: renderChannels, shows: renderShows, server: renderServer })[A.tab]();
}

async function refresh() {
  [A.sessions, A.devices] = await Promise.all([api('GET', 'sessions'), api('GET', 'devices')]);
  if (A.tab === 'live') renderLive();
  if (A.tab === 'devices') renderDevices();
}

async function loadConfig() {
  A.cfg = await api('GET', 'config');
  $('title').textContent = A.cfg.serverName + ' · Admin';
  setDirty(false);
}

async function save() {
  const body = { ...A.cfg };
  if (A.newPassword) body.newPassword = A.newPassword;
  A.cfg = await api('PUT', 'config', body);
  A.newPassword = '';
  if (A.editUser) A.editUser = A.cfg.users.find((u) => u.name === A.editUser.name && u.show === A.editUser.show) || null;
  setDirty(false);
  msg('Saved. Connected clients were updated.');
  A.devCards.clear(); $('tab-devices').dataset.ids = '';
  renderTab();
}

async function start() {
  $('login').classList.add('hidden');
  $('main').classList.remove('hidden');
  await loadConfig();
  await refresh();
  renderTab();
  setInterval(() => refresh().catch(() => {}), 2000);
}

$('loginForm').addEventListener('submit', run(async (e) => {
  e.preventDefault();
  const body = $('ap').value ? { password: $('ap').value } : { user: $('au').value, pin: $('apin').value };
  const r = await fetch('api/admin/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
  if (!r.ok) { $('loginErr').textContent = (await r.text()).trim(); return; }
  A.token = (await r.json()).token;
  sessionStorage.setItem('ol.admin', A.token);
  await start();
}));
for (const b of document.querySelectorAll('[data-tab]')) b.addEventListener('click', () => { A.tab = b.dataset.tab; renderTab(); });
$('saveBtn').addEventListener('click', run(save));
$('showSel').addEventListener('change', (e) => { A.show = +e.target.value; saveShowPref(); A.editUser = null; $('tab-live').replaceChildren(); renderTab(); });
window.addEventListener('beforeunload', (e) => { if (A.dirty) e.preventDefault(); });
if (A.token) start().catch(() => { sessionStorage.removeItem('ol.admin'); location.reload(); });
