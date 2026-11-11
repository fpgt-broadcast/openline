// OpenLine admin page: live sessions, beltpack control, users, channels, server.
import { provision, bleSupported } from './ble.js';

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

const A = { token: sessionStorage.getItem('ol.admin') || '', cfg: null, dirty: false, tab: 'live', sessions: [], devices: [], editUser: null, devCards: new Map() };

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

function setDirty(d = true) {
  A.dirty = d;
  $('dirty').classList.toggle('hidden', !d);
  $('saveBtn').disabled = !d;
}

const chName = (id) => A.cfg.channels.find((c) => c.id === id)?.name || '#' + id;
const chColor = (id) => A.cfg.channels.find((c) => c.id === id)?.color || '#666';
const baseURL = () => (A.cfg.publicURL || location.origin).replace(/\/$/, '');
const fmtAgo = (t) => { const s = Math.round((Date.now() - new Date(t)) / 1000); return s < 90 ? s + 's' : s < 5400 ? Math.round(s / 60) + 'm' : Math.round(s / 3600) + 'h'; };

// ---------------- Live ----------------
function remote(sid, op) { return api('POST', `sessions/${sid}/remote`, op).then(refresh); }

function renderLive() {
  const rows = A.sessions.map((s) => h('tr', {},
    h('td', {}, h('b', {}, s.user), h('div', { class: 'small muted' }, `${s.client}${s.device ? ' · ' + s.device : ''}`)),
    h('td', { class: 'small muted' }, s.remote, h('br'), fmtAgo(s.connected)),
    h('td', {}, h('div', { class: 'row', style: { flexWrap: 'wrap', gap: '4px' } }, s.channels.map((id) => {
      const t = s.talk.includes(id), l = s.listen.includes(id);
      return h('span', { class: 'chip', style: { borderLeft: `3px solid ${chColor(id)}` } }, chName(id), ' ',
        h('button', { class: t ? 'on' : 'ghost', style: { minHeight: '24px', color: t ? '#ffb4b6' : '' }, title: 'Force talk', onclick: run(() => remote(s.sid, { op: 'talk', ch: id, on: !t })) }, 'T'),
        h('button', { class: l ? 'on' : 'ghost', style: { minHeight: '24px' }, title: 'Force listen', onclick: run(() => remote(s.sid, { op: 'listen', ch: id, on: !l })) }, 'L'));
    }))),
    h('td', {}, s.sending ? h('span', { class: 'pill bad' }, 'TALKING') : s.talk.length ? h('span', { class: 'pill warn' }, 'latched') : '',
      s.tally === 'program' ? h('span', { class: 'pill bad', style: { marginLeft: '4px' } }, 'ON AIR') : s.tally === 'preview' ? h('span', { class: 'pill ok', style: { marginLeft: '4px' } }, 'PREVIEW') : ''),
    h('td', {}, h('div', { class: 'row' },
      h('button', { class: s.muted ? 'on' : '', onclick: run(() => remote(s.sid, { op: 'mute', on: !s.muted })) }, s.muted ? 'Unmute mic' : 'Mute mic'),
      h('button', { onclick: run(() => remote(s.sid, { op: 'identify' })) }, 'Identify'),
      h('button', { onclick: run(async () => { if (confirm(`Disconnect ${s.user}?`)) await remote(s.sid, { op: 'kick' }); }) }, 'Kick'))),
  ));
  $('tab-live').replaceChildren(
    h('div', { class: 'small muted', style: { marginBottom: '8px' } }, `${A.sessions.length} connected · T/L force talk/listen per channel`),
    h('div', { class: 'tablebox' }, h('table', {},
      h('thead', {}, h('tr', {}, ['User', 'From', 'Channels', 'State', 'Control'].map((t) => h('th', {}, t)))),
      h('tbody', {}, rows.length ? rows : h('tr', {}, h('td', { colspan: 5, class: 'muted' }, 'Nobody connected'))))));
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
    A.cfg.users.map((u) => h('option', { value: u.id, selected: u.id === d.userId }, u.name + (u.bridge ? ' (bridge)' : ''))));
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
      h('button', { style: { marginTop: '8px' }, onclick: run(async () => {
        const body = { mode: f.mode.value, dhcp: f.dhcp.value === '1', ip: f.ip.value.trim(), mask: f.mask.value.trim(), gateway: f.gateway.value.trim(), dns: f.dns.value.trim(), ssid: f.ssid.value, pass: f.pass.value, server: f.server.value.trim() };
        if (!confirm(`Apply network settings to ${d.name}? It will reboot${d.online ? '' : ' (when it next connects you must re-apply — it is offline)'}.`)) return;
        await api('PUT', `devices/${d.id}/net`, body);
        msg(d.online ? 'Network settings sent — beltpack is rebooting' : 'Saved (beltpack offline; settings are sent when you apply while it is online)');
      }) }, 'Apply network & reboot')),
    h('button', { class: 'ghost', onclick: run(async () => { if (confirm(`Forget ${d.name}?`)) { await api('DELETE', `devices/${d.id}`); A.devCards.clear(); await refresh(); } }) }, 'Forget device'),
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
    h('button', { onclick: run(async () => { if (confirm('Reboot beltpack?')) await remote(s.sid, { op: 'reboot' }); }) }, 'Reboot'),
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
    box.replaceChildren(
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
  f.user = h('select', {}, h('option', { value: '' }, '— assign later in admin —'), A.cfg.users.map((u) => h('option', { value: u.token }, u.name)));
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
  const users = A.cfg.users;
  const table = h('div', { class: 'tablebox' }, h('table', {},
    h('thead', {}, h('tr', {}, ['Name', 'PIN', 'vMix tally', 'Role', 'Channels (key order)', ''].map((t) => h('th', {}, t)))),
    h('tbody', {}, users.map((u) => h('tr', {},
      h('td', {}, h('input', { type: 'text', value: u.name, oninput: (e) => { u.name = e.target.value; setDirty(); } })),
      h('td', { style: { width: '90px' } }, h('input', { type: 'text', value: u.pin || '', placeholder: 'none', inputmode: 'numeric', oninput: (e) => { u.pin = e.target.value.trim(); setDirty(); } })),
      h('td', { style: { width: '90px' } }, h('input', { type: 'text', value: (u.tally || []).join(', '), placeholder: 'none', inputmode: 'numeric',
        title: 'vMix input number(s) this person is on, e.g. 2 or 2, 5', 'aria-label': u.name + ' vMix tally input',
        oninput: (e) => { u.tally = parseTally(e.target.value); setDirty(); } })),
      h('td', { class: 'small' },
        h('label', { style: { margin: 0, color: 'inherit' } }, h('input', { type: 'checkbox', checked: !!u.admin, onchange: (e) => { u.admin = e.target.checked; setDirty(); } }), ' admin'),
        h('label', { style: { margin: 0, color: 'inherit' } }, h('input', { type: 'checkbox', checked: !!u.bridge, onchange: (e) => { u.bridge = e.target.checked; setDirty(); } }), ' NDI bridge')),
      h('td', { class: 'small' }, u.channels.map((a, i) => h('span', { class: 'chip', style: { borderLeft: `3px solid ${chColor(a.ch)}`, marginRight: '4px' } }, `${i + 1} ${chName(a.ch)} · ${accessLabel(a)}`))),
      h('td', {}, h('div', { class: 'row' },
        h('button', { onclick: () => { A.editUser = u; renderUsers(); } }, 'Edit'),
        h('button', { onclick: () => { A.editUser = u; A.showInvite = true; renderUsers(); } }, 'Invite'))),
    )))));
  $('tab-users').replaceChildren(
    h('div', { class: 'row', style: { marginBottom: '8px' } },
      h('div', { class: 'grow small muted' }, 'Each user is a login (a person, a beltpack role, or an NDI bridge link). Names are yours to choose. vMix tally = the vMix input number(s) that person is on. Channel order = beltpack key order.'),
      h('button', { onclick: () => { const u = { name: 'New user ' + (users.length + 1), pin: '', channels: [], token: '' }; users.push(u); A.editUser = u; setDirty(); renderUsers(); } }, 'Add user')),
    table, A.editUser ? userEditor(A.editUser) : null);
}

function userEditor(u) {
  const rows = A.cfg.channels.map((c) => {
    const i = u.channels.findIndex((a) => a.ch === c.id);
    return { c, a: i >= 0 ? u.channels[i] : null, i };
  }).sort((x, y) => (x.i < 0 ? 999 : x.i) - (y.i < 0 ? 999 : y.i));
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
  const link = u.token ? `${baseURL()}/#t=${u.token}` : '';
  return h('div', { class: 'panel', style: { marginTop: '12px' } },
    h('div', { class: 'row' }, h('b', { class: 'grow' }, 'Edit ', u.name), h('button', { class: 'ghost', onclick: () => { A.editUser = null; A.showInvite = false; renderUsers(); } }, 'Close')),
    h('div', { class: 'tablebox', style: { marginTop: '8px' } }, h('table', {}, h('tbody', {}, rows.map(({ c, a }, n) => h('tr', {},
      h('td', { style: { width: '40px' } }, a ? String(u.channels.indexOf(a) + 1) : ''),
      h('td', {}, h('span', { class: 'chip', style: { borderLeft: `3px solid ${c.color}` } }, c.name), c.listenOnly ? h('span', { class: 'small muted' }, ' listen-only') : ''),
      h('td', {}, h('select', { onchange: (e) => set(c, e.target.value) },
        [['', 'No access'], ['L', 'Listen'], ['TL', 'Talk + Listen'], ['T', 'Talk only']].map(([v, t]) =>
          h('option', { value: v, selected: (a ? (a.talk ? 'T' : '') + (a.listen ? 'L' : '') : '') === v }, t)))),
      h('td', {}, a ? h('div', { class: 'row' }, h('button', { onclick: () => move(c.id, -1) }, '↑'), h('button', { onclick: () => move(c.id, 1) }, '↓')) : ''),
    ))))),
    h('label', {}, 'vMix tally inputs (input numbers this user is, e.g. camera on input 2 → "2")'),
    h('input', { type: 'text', inputmode: 'numeric', placeholder: 'none', value: (u.tally || []).join(', '),
      oninput: (e) => { u.tally = parseTally(e.target.value); setDirty(); } }),
    h('div', { class: 'row', style: { marginTop: '10px', flexWrap: 'wrap' } },
      h('button', { onclick: () => { u.token = ''; setDirty(); msg('New token is generated when you save. Old invite links and devices using it stop working.'); } }, 'Regenerate token'),
      h('button', { class: 'ghost', onclick: () => { if (confirm(`Delete ${u.name}?`)) { A.cfg.users.splice(A.cfg.users.indexOf(u), 1); A.editUser = null; setDirty(); renderUsers(); } } }, 'Delete user')),
    link ? h('div', { style: { marginTop: '12px' } },
      h('label', {}, u.bridge ? 'Bridge token (put in bridge.json)' : 'Invite link — opens the intercom already signed in'),
      h('input', { type: 'text', readOnly: true, value: u.bridge ? u.token : link, onclick: (e) => e.target.select() }),
      !u.bridge && A.showInvite ? h('img', { class: 'qr', style: { marginTop: '8px' }, alt: 'QR code for invite link', src: `api/qr?k=${A.token}&text=${encodeURIComponent(link)}` }) : null)
      : h('div', { class: 'small muted', style: { marginTop: '8px' } }, 'Save to generate this user\'s invite link / token.'));
}

// ---------------- Channels ----------------
function renderChannels() {
  const chs = A.cfg.channels;
  const move = (i, d) => { const j = i + d; if (j < 0 || j >= chs.length) return; [chs[i], chs[j]] = [chs[j], chs[i]]; setDirty(); renderChannels(); };
  $('tab-channels').replaceChildren(
    h('div', { class: 'row', style: { marginBottom: '8px' } },
      h('div', { class: 'grow small muted' }, 'Listen-only channels (program, NDI feeds) can only be talked on by NDI bridge users. Program channels are dipped 12 dB while anyone talks.'),
      h('button', { onclick: () => { const id = Math.max(0, ...chs.map((c) => c.id)) + 1; chs.push({ id, name: 'Channel ' + id, color: '#4a90d9' }); setDirty(); renderChannels(); } }, 'Add channel')),
    h('div', { class: 'tablebox' }, h('table', {},
      h('thead', {}, h('tr', {}, ['ID', 'Name', 'Color', 'Listen-only', 'Program (duck)', ''].map((t) => h('th', {}, t)))),
      h('tbody', {}, chs.map((c, i) => h('tr', {},
        h('td', { class: 'muted' }, String(c.id)),
        h('td', {}, h('input', { type: 'text', value: c.name, oninput: (e) => { c.name = e.target.value; setDirty(); } })),
        h('td', {}, h('input', { type: 'color', class: 'swatch', value: c.color, oninput: (e) => { c.color = e.target.value; setDirty(); } })),
        h('td', {}, h('input', { type: 'checkbox', checked: !!c.listenOnly, onchange: (e) => { c.listenOnly = e.target.checked; setDirty(); } })),
        h('td', {}, h('input', { type: 'checkbox', checked: !!c.program, onchange: (e) => { c.program = e.target.checked; setDirty(); } })),
        h('td', {}, h('div', { class: 'row' },
          h('button', { onclick: () => move(i, -1) }, '↑'), h('button', { onclick: () => move(i, 1) }, '↓'),
          h('button', { class: 'ghost', onclick: () => { if (confirm(`Delete ${c.name}? Users lose access to it.`)) { chs.splice(i, 1); setDirty(); renderChannels(); } } }, 'Delete'))),
      ))))));
}

// ---------------- Server ----------------
function renderServer() {
  const c = A.cfg;
  $('tab-server').replaceChildren(h('div', { class: 'panel', style: { maxWidth: '560px' } },
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
  for (const t of ['live', 'devices', 'users', 'channels', 'server']) $('tab-' + t).classList.toggle('hidden', t !== A.tab);
  ({ live: renderLive, devices: renderDevices, users: renderUsers, channels: renderChannels, server: renderServer })[A.tab]();
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
  if (A.editUser) A.editUser = A.cfg.users.find((u) => u.name === A.editUser.name) || null;
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
window.addEventListener('beforeunload', (e) => { if (A.dirty) e.preventDefault(); });
if (A.token) start().catch(() => { sessionStorage.removeItem('ol.admin'); location.reload(); });
