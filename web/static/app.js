// OpenLine web client: login, channel list, PTT, detail + settings sheets,
// and the Bluetooth beltpack bridge.
import { AudioEngine, codecSupport, insecureMessage } from './audio.js';
import { Beltpack, bleSupported } from './ble.js';

const $ = (id) => document.getElementById(id);
function h(tag, attrs = {}, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k.startsWith('on')) el.addEventListener(k.slice(2).toLowerCase(), v);
    else if (k === 'style' && typeof v === 'object') {
      for (const [sk, sv] of Object.entries(v)) {
        if (sk.startsWith('--')) el.style.setProperty(sk, sv); else el.style[sk] = sv;
      }
    }
    else if (k === 'class') el.className = v;
    else if (k in el && typeof v !== 'string') el[k] = v;
    else el.setAttribute(k, v === true ? '' : v);
  }
  for (const kid of kids.flat()) if (kid !== null && kid !== undefined && kid !== false) el.append(kid);
  return el;
}
const store = {
  get(k, d) { try { const v = localStorage.getItem('ol.' + k); return v == null ? d : JSON.parse(v); } catch { return d; } },
  set(k, v) { try { localStorage.setItem('ol.' + k, JSON.stringify(v)); } catch {} },
  del(k) { try { localStorage.removeItem('ol.' + k); } catch {} },
};

const S = {
  ws: null, welcome: null, channels: [],
  state: new Map(),                 // ch id -> {talkers, calls, members}
  talk: new Set(), listen: new Set(), muted: false, tally: 'off',
  tallyView: store.get('tallyView', false), serverTallyScreen: null,
  targets: new Set(store.get('targets', [])),
  vols: store.get('vols', {}),      // ch id -> 0..100
  listenPref: store.get('listenPref', {}),
  master: store.get('master', 75),
  sidMask: new Map(), sidLevel: {}, lastNonProgram: 0,
  rtt: null, latched: false, pttT0: 0, pttIgnoreUp: false, pttActive: false, calling: false,
  audio: null, audioStarting: null, belt: null, beltEnc: null, beltSeq: 0,
  creds: store.get('creds', null), reconnectDelay: 1000, everWelcomed: false, sheet: null,
};

// ---------------- connection ----------------
function connect() {
  if (S.ws) { try { S.ws.close(); } catch {} }
  const ws = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/ws');
  ws.binaryType = 'arraybuffer';
  S.ws = ws;
  setDot('warn');
  ws.onopen = () => ws.send(JSON.stringify({ t: 'hello', ver: 1, client: 'web', ...S.creds }));
  ws.onmessage = (e) => {
    if (typeof e.data !== 'string') return onAudio(e.data);
    let m; try { m = JSON.parse(e.data); } catch { return; }
    onControl(m);
  };
  ws.onclose = (e) => {
    if (S.ws !== ws) return;
    setDot('bad');
    S.talk.clear();
    if (e.code === 4001 || (!S.everWelcomed && e.code !== 1006)) {
      // bad credentials
      store.del('creds');
      S.creds = null;
      showLogin('Login failed. Check name and PIN.');
      return;
    }
    banner('Disconnected — reconnecting…', 'warn');
    clearTimeout(S.reconnectTimer);
    S.reconnectTimer = setTimeout(connect, S.reconnectDelay);
    S.reconnectDelay = Math.min(S.reconnectDelay * 1.6, 10000);
  };
}

function send(m) { if (S.ws && S.ws.readyState === 1) S.ws.send(JSON.stringify(m)); }

function onControl(m) {
  switch (m.t) {
    case 'welcome': {
      S.welcome = m; S.channels = m.channels; S.everWelcomed = true; S.reconnectDelay = 1000;
      S.talk = new Set(m.talk); S.listen = new Set(m.listen); S.muted = !!m.muted;
      store.set('creds', S.creds);
      // re-apply local listen preferences
      for (const ch of S.channels) {
        const pref = S.listenPref[ch.id];
        if (pref === false && S.listen.has(ch.id)) send({ t: 'listen', ch: ch.id, on: false });
      }
      for (const id of [...S.targets]) {
        const ch = chan(id);
        if (!ch || !ch.canTalk) S.targets.delete(id);
      }
      // Admin's per-user "tally screen" default: apply when it changes (or on first connect).
      if (S.serverTallyScreen !== !!m.tallyScreen) {
        if (S.serverTallyScreen !== null || m.tallyScreen) setTallyView(!!m.tallyScreen);
        S.serverTallyScreen = !!m.tallyScreen;
      }
      reportView();
      $('hUser').textContent = m.user;
      $('hServer').textContent = '· ' + m.server;
      document.title = m.server + ' · OpenLine';
      setDot('ok');
      banner(null);
      if (S.audio) showApp();
      else if (S.audioStarting) S.audioStarting.then(() => { showApp(); renderAll(); }, showStart);
      else showStart();
      renderAll();
      break;
    }
    case 'self':
      S.talk = new Set(m.talk); S.listen = new Set(m.listen); S.muted = !!m.muted;
      if (S.talk.size === 0) { S.latched = false; S.pttActive = false; }
      updateTransmit(); renderAll();
      break;
    case 'state':
      for (const c of m.channels) S.state.set(c.id, c);
      renderAll();
      break;
    case 'tally':
      S.tally = m.state;
      renderTally();
      syncBelt();
      break;
    case 'pong':
      S.rtt = Math.round(performance.now() - m.ts);
      $('hRtt').textContent = S.rtt + ' ms';
      break;
    case 'remote':
      if (m.op === 'vol') setVol(m.ch, m.v);
      if (m.op === 'tallyscreen') setTallyView(!!m.on);
      if (m.op === 'identify') { document.body.classList.remove('flash'); void document.body.offsetWidth; document.body.classList.add('flash'); }
      break;
    case 'error':
      banner(m.msg, 'bad', 3000);
      break;
  }
}

function onAudio(buf) {
  if (!S.audio || !S.audio.ctx) return;
  const dv = new DataView(buf);
  if (dv.byteLength < 10) return;
  const mask = dv.getUint32(5);
  let nonProgram = false;
  S.channels.forEach((ch, i) => { if ((mask >>> i) & 1 && !ch.program) nonProgram = true; });
  if (nonProgram) S.lastNonProgram = performance.now();
  const r = S.audio.receive(buf, gainFor);
  if (r) S.sidMask.set(r.sid, r.mask);
}

function volGain(id) {
  const v = S.vols[id] ?? 75;
  return Math.pow(v / 75, 2);
}
function gainFor(mask) {
  let g = 0, onlyProgram = true;
  S.channels.forEach((ch, i) => {
    if ((mask >>> i) & 1) { g = Math.max(g, volGain(ch.id)); if (!ch.program) onlyProgram = false; }
  });
  if (onlyProgram && performance.now() - S.lastNonProgram < 600) g *= 0.25; // duck program -12 dB
  return g * Math.pow(S.master / 75, 2);
}
const chan = (id) => S.channels.find((c) => c.id === id);
const chanIdx = (id) => S.channels.findIndex((c) => c.id === id);

function setVol(id, v) {
  S.vols[id] = Math.max(0, Math.min(100, v | 0));
  store.set('vols', S.vols);
  if (S.audio) S.audio.regain((sid) => gainFor(S.sidMask.get(sid) || 0));
  renderAll();
}

function setListen(id, on) {
  S.listenPref[id] = on;
  store.set('listenPref', S.listenPref);
  send({ t: 'listen', ch: id, on });
}

// Beltpack audio route: 'belt' = headset on the beltpack; 'phone' = beltpack is a
// remote control only and audio uses this device (AirPods, BT/wired headset...).
const beltAudio = () => store.get('beltAudio', 'belt');
const beltCarriesAudio = () => !!S.belt && beltAudio() === 'belt';

function updateTransmit() {
  if (S.audio) S.audio.transmit = S.talk.size > 0 && !beltCarriesAudio();
}

// ---------------- PTT ----------------
function setTalkTargets(on) {
  if (on) for (const id of S.targets) send({ t: 'talk', ch: id, on: true });
  else for (const id of new Set([...S.talk, ...S.targets])) send({ t: 'talk', ch: id, on: false });
  S.pttActive = on;
  renderPTT();
}
function pttDown() {
  if (S.muted || S.targets.size === 0) return;
  S.pttT0 = performance.now();
  if (S.latched || S.talk.size > 0) { S.latched = false; S.pttIgnoreUp = true; setTalkTargets(false); return; }
  S.pttIgnoreUp = false;
  setTalkTargets(true);
}
function pttUp() {
  if (S.pttIgnoreUp) { S.pttIgnoreUp = false; return; }
  if (!S.pttActive) return;
  if (performance.now() - S.pttT0 < 300) { S.latched = true; renderPTT(); return; }
  setTalkTargets(false);
}
function toggleTarget(id) {
  const ch = chan(id);
  if (!ch || !ch.canTalk) return;
  const live = S.pttActive || S.talk.size > 0;
  if (S.targets.has(id)) { S.targets.delete(id); if (live) send({ t: 'talk', ch: id, on: false }); }
  else { S.targets.add(id); if (live) send({ t: 'talk', ch: id, on: true }); }
  store.set('targets', [...S.targets]);
  renderAll();
}
function setCall(on) {
  S.calling = on;
  for (const id of S.targets) send({ t: 'call', ch: id, on });
  $('callBtn').classList.toggle('live', on);
}

// ---------------- rendering ----------------
function activityText(id) {
  const st = S.state.get(id);
  const me = S.welcome && S.welcome.sid;
  if (!st) return { text: '', cls: '' };
  const calls = st.calls.filter((t) => t.sid !== me);
  if (calls.length) return { text: 'CALL · ' + calls.map((t) => t.name).join(', '), cls: 'call' };
  const talkers = st.talkers.filter((t) => t.sid !== me);
  if (talkers.length) return { text: talkers.map((t) => t.name).join(', ') + ' talking', cls: 'talking' };
  if (S.talk.has(id)) return { text: 'you are talking', cls: '' };
  return { text: st.members.length + ' listening', cls: '' };
}

function channelLevel(id) {
  const i = chanIdx(id);
  let lvl = 0;
  for (const [sid, mask] of S.sidMask) if ((mask >>> i) & 1) lvl = Math.max(lvl, S.sidLevel[sid] || 0);
  return lvl;
}

function renderAll() {
  renderChannels(); renderPTT(); renderBanner(); renderTallyView();
  if (S.sheet && S.sheet.kind === 'channel') renderChannelSheet(S.sheet.id);
  syncBelt();
}

function renderChannels() {
  const box = $('channels');
  box.replaceChildren(...S.channels.map((ch) => {
    const act = activityText(ch.id);
    const calling = act.cls === 'call';
    const sel = S.targets.has(ch.id);
    const listening = S.listen.has(ch.id);
    return h('div', { class: 'ch' + (calling ? ' calling' : ''), style: { '--c': ch.color }, 'data-ch': ch.id },
      h('button', {
        class: 'tgt' + (sel ? ' sel' : '') + (S.talk.has(ch.id) ? ' live' : ''),
        disabled: !ch.canTalk, 'aria-pressed': String(sel), 'aria-label': 'Talk to ' + ch.name,
        onclick: () => toggleTarget(ch.id),
      }, sel ? '✓' : ''),
      h('button', { class: 'info', onclick: () => openChannelSheet(ch.id), 'aria-label': ch.name + ' details' },
        h('span', { class: 'name' }, ch.name),
        h('span', { class: 'act ' + act.cls }, ch.listenOnly && !act.text.includes('talk') && !calling ? (ch.program ? 'program · ' : 'listen only · ') + act.text : act.text)),
      h('div', { class: 'lvl', 'aria-hidden': 'true' }, h('i', { 'data-lvl': ch.id })),
      h('span', { class: 'vol' }, String(S.vols[ch.id] ?? 75)),
      h('button', {
        class: 'lsn' + (listening ? ' on' : ' off'), disabled: !ch.canListen, 'aria-pressed': String(listening),
        onclick: () => setListen(ch.id, !listening),
      }, listening ? 'ON' : 'OFF'),
    );
  }));
}

function setTallyView(on) {
  S.tallyView = on;
  store.set('tallyView', on);
  if (on) closeSheet();
  reportView();
  renderTallyView();
}
function reportView() { send({ t: 'view', on: !!S.tallyView }); }

function renderTallyView() {
  const v = $('tallyView');
  if (!v) return;
  v.classList.toggle('hidden', !S.tallyView);
  if (!S.tallyView) return;
  const t = S.tally;
  v.classList.remove('off', 'program', 'preview');
  v.classList.add(t === 'program' || t === 'preview' ? t : 'off');
  $('tvState').textContent = t === 'program' ? 'ON AIR' : t === 'preview' ? 'PREVIEW' : 'OFF';
  $('tvUser').textContent = S.welcome ? S.welcome.user : '';
  $('tvDot').className = $('hDot').className;
  // who is talking to me right now
  const me = S.welcome && S.welcome.sid, names = new Set();
  for (const ch of S.channels) {
    const st = S.state.get(ch.id);
    if (st && S.listen.has(ch.id)) for (const tk of st.talkers) if (tk.sid !== me && !ch.program) names.add(tk.name);
  }
  $('tvAct').textContent = names.size ? [...names].join(', ') + ' talking' : '';
  const btn = $('tvPtt'), live = S.talk.size > 0;
  btn.classList.toggle('live', live);
  btn.disabled = S.muted || (S.targets.size === 0 && !live);
  const targets = [...S.targets].map((id) => chan(id)?.name).filter(Boolean);
  btn.querySelector('.big').textContent = S.muted ? 'MIC MUTED' : live ? (S.latched ? 'TALKING · LATCHED' : 'TALKING') : 'HOLD TO TALK';
  $('tvPttSub').textContent = S.muted ? 'muted by admin' : targets.length ? 'to ' + targets.join(' + ') : 'Exit and pick channels to talk to';
}

function renderTally() {
  renderTallyView();
  const el = $('tally');
  const t = S.tally;
  el.className = 'tally ' + t + (t === 'off' ? ' hidden' : '');
  el.textContent = t === 'program' ? '● ON AIR' : t === 'preview' ? 'PREVIEW' : '';
  document.body.classList.toggle('tally-program', t === 'program');
  document.body.classList.toggle('tally-preview', t === 'preview');
}

function renderPTT() {
  const btn = $('ptt');
  const live = S.talk.size > 0;
  btn.classList.toggle('live', live);
  btn.classList.toggle('latched', live && S.latched);
  btn.disabled = S.muted || (S.targets.size === 0 && !live);
  const names = [...S.targets].map((id) => chan(id)?.name).filter(Boolean);
  btn.querySelector('.big').textContent = S.muted ? 'MIC MUTED' : live ? 'TALKING' : 'HOLD TO TALK';
  $('pttSub').textContent = S.muted ? 'muted by admin'
    : names.length ? (live ? 'to ' : 'tap = latch · hold = talk — to ') + names.join(' + ') : 'select channels above';
  $('callBtn').disabled = S.targets.size === 0;
}

let bannerTimer = null;
function banner(text, kind = 'warn', ms = 0) {
  const b = $('banner');
  clearTimeout(bannerTimer);
  if (!text) { S.bannerText = null; renderBanner(); return; }
  S.bannerText = { text, kind };
  renderBanner();
  if (ms) bannerTimer = setTimeout(() => { S.bannerText = null; renderBanner(); }, ms);
}
function renderBanner() {
  const b = $('banner');
  const t = S.muted ? { text: 'Your mic is muted by the admin', kind: 'bad' } : S.bannerText;
  b.classList.toggle('hidden', !t);
  if (t) { b.textContent = t.text; b.className = 'banner ' + t.kind; }
}

function setDot(kind) { const d = $('hDot'); d.className = 'dot ' + kind; }

function levelTick() {
  for (const el of document.querySelectorAll('[data-lvl]')) {
    const v = Math.min(1, channelLevel(+el.dataset.lvl) * 1.6);
    el.style.height = (v * 100).toFixed(0) + '%';
  }
  requestAnimationFrame(levelTick);
}

// ---------------- sheets ----------------
function openSheet(kind, id, render) {
  S.sheet = { kind, id };
  const bg = $('sheet');
  bg.classList.remove('hidden');
  bg.onclick = (e) => { if (e.target === bg) closeSheet(); };
  render();
}
function closeSheet() { S.sheet = null; $('sheet').classList.add('hidden'); $('sheet').replaceChildren(); }

function openChannelSheet(id) { openSheet('channel', id, () => renderChannelSheet(id)); }
function renderChannelSheet(id) {
  const ch = chan(id);
  if (!ch) return closeSheet();
  const st = S.state.get(id) || { talkers: [], calls: [], members: [] };
  const v = S.vols[id] ?? 75;
  const db = v === 0 ? '−∞' : (20 * Math.log10(volGain(id))).toFixed(1);
  const listening = S.listen.has(id);
  const old = $('sheet').querySelector('input[type=range]');
  const dragging = old && document.activeElement === old;
  if (dragging) { old.parentElement.querySelector('.dbv').textContent = db + ' dB'; return; }
  const call = h('button', { disabled: !ch.canTalk && ch.listenOnly }, 'HOLD TO CALL');
  // The sheet re-renders on every state change, so release is tracked globally.
  call.addEventListener('pointerdown', () => { S.sheetCall = id; send({ t: 'call', ch: id, on: true }); });
  const chip = (t, cls) => h('span', { class: 'chip ' + (cls || '') }, t.name + (t.sid === S.welcome.sid ? ' (you)' : ''));
  $('sheet').replaceChildren(h('div', { class: 'sheet', style: { '--c': ch.color }, role: 'dialog', 'aria-label': ch.name },
    h('div', { class: 'row' }, h('h2', { class: 'grow' }, ch.name), h('button', { class: 'ghost', onclick: closeSheet }, 'Close')),
    h('div', { class: 'small muted' }, ch.program ? 'Program feed — automatically dipped 12 dB while anyone talks' : ch.listenOnly ? 'Listen-only channel' : 'Party line'),
    h('div', { class: 'grid2' },
      h('button', { class: listening ? 'on' : '', disabled: !ch.canListen, onclick: () => setListen(id, !listening) }, listening ? 'Listening' : 'Not listening'),
      h('button', { class: S.targets.has(id) ? 'on' : '', disabled: !ch.canTalk, onclick: () => toggleTarget(id) }, S.targets.has(id) ? 'Talk target ✓' : 'Add to talk'),
    ),
    h('h3', {}, 'Level'),
    h('div', { class: 'row' },
      h('input', { type: 'range', min: 0, max: 100, value: v, 'aria-label': ch.name + ' volume', oninput: (e) => setVol(id, +e.target.value) }),
      h('span', { class: 'dbv small muted', style: { width: '64px', textAlign: 'right' } }, db + ' dB')),
    h('div', { class: 'grid2' }, call, h('button', { class: 'ghost', onclick: () => setVol(id, 75) }, 'Reset to 0 dB')),
    h('h3', {}, 'Talking'),
    h('div', { class: 'people' }, st.talkers.length ? st.talkers.map((t) => chip(t, 'talk')) : h('span', { class: 'small muted' }, 'nobody')),
    st.calls.length ? [h('h3', {}, 'Calling'), h('div', { class: 'people' }, st.calls.map((t) => chip(t, 'call')))] : null,
    h('h3', {}, `Connected (${st.members.length})`),
    h('div', { class: 'people' }, st.members.length ? st.members.map((t) => chip(t)) : h('span', { class: 'small muted' }, 'nobody listening')),
  ));
}

async function openSettings() {
  let mics = [], outs = [];
  try {
    const devs = await navigator.mediaDevices.enumerateDevices();
    mics = devs.filter((d) => d.kind === 'audioinput');
    outs = devs.filter((d) => d.kind === 'audiooutput');
  } catch {}
  const canSink = S.audio && S.audio.ctx && 'setSinkId' in S.audio.ctx;
  const opt = (d, sel) => h('option', { value: d.deviceId, selected: d.deviceId === sel }, d.label || d.kind);
  openSheet('settings', null, () => {
    $('sheet').replaceChildren(h('div', { class: 'sheet', role: 'dialog', 'aria-label': 'Settings' },
      h('div', { class: 'row' }, h('h2', { class: 'grow' }, 'Settings'), h('button', { class: 'ghost', onclick: closeSheet }, 'Close')),
      h('label', { for: 'sMic' }, 'Microphone'),
      h('select', { id: 'sMic', disabled: !!S.belt, onchange: async (e) => { store.set('mic', e.target.value); await S.audio?.setMic(e.target.value, store.get('aec', true)); } },
        mics.map((d) => opt(d, store.get('mic', ''))), h('option', { value: 'none', selected: store.get('mic', '') === 'none' }, 'No microphone (listen only)')),
      canSink ? [h('label', { for: 'sOut' }, 'Output'),
        h('select', { id: 'sOut', onchange: (e) => { store.set('out', e.target.value); S.audio.setOutput(e.target.value); } }, outs.map((d) => opt(d, store.get('out', ''))))] : null,
      h('label', {}, h('input', { type: 'checkbox', checked: store.get('aec', true), onchange: async (e) => { store.set('aec', e.target.checked); await S.audio?.setMic(store.get('mic', ''), e.target.checked); } }), ' Echo cancellation (turn off with headsets for best quality)'),
      h('label', { for: 'sMaster' }, 'Master volume'),
      h('input', { id: 'sMaster', type: 'range', min: 0, max: 100, value: S.master, oninput: (e) => { S.master = +e.target.value; store.set('master', S.master); S.audio?.regain((sid) => gainFor(S.sidMask.get(sid) || 0)); } }),
      h('label', { for: 'sJit' }, 'Network buffer'),
      h('select', { id: 'sJit', onchange: (e) => { store.set('jitter', +e.target.value); S.audio?.setJitter(+e.target.value); } },
        [[40, 'Low latency (good networks)'], [60, 'Normal'], [120, 'Safe (bad cellular / Wi‑Fi)']].map(([v, t]) => h('option', { value: v, selected: store.get('jitter', 60) === v }, t))),
      h('h3', {}, 'Beltpack (Bluetooth)'),
      beltSection(),
      h('button', { style: { marginTop: '12px', width: '100%' }, onclick: () => { closeSheet(); setTallyView(true); } }, 'Full-screen tally view'),
      h('button', { style: { marginTop: '8px', width: '100%' }, onclick: () => { closeSheet(); openGuide(0); } }, 'Show quick start guide'),
      h('h3', {}, 'Headphone button'),
      h('label', {}, h('input', { type: 'checkbox', checked: store.get('mediaPTT', false), onchange: (e) => { store.set('mediaPTT', e.target.checked); setupMediaPTT(); } }),
        ' Headphone play/pause button toggles talk (AirPods stem press, headset button). Experimental — depends on browser/OS.'),
      h('h3', {}, 'Keyboard / foot pedal'),
      h('div', { class: 'small muted' }, 'Space: hold to talk / tap to latch · 1–9: toggle talk target · C: hold to call. USB foot pedals that send Space work as a PTT.'),
      h('div', { class: 'grid2' },
        h('a', { href: 'admin.html', class: 'small', style: { alignSelf: 'center' } }, 'Admin page'),
        h('button', { onclick: logout }, 'Sign out')),
    ));
  });
}

function beltSection() {
  if (!bleSupported()) return h('div', { class: 'small muted' }, 'This browser has no Web Bluetooth. Use Chrome or Edge on desktop/Android (on iPhone use the Bluefy browser), or run the beltpack in Wi‑Fi/Ethernet mode.');
  if (!S.belt) return h('button', { onclick: connectBelt }, 'Connect beltpack');
  const viaBelt = beltAudio() === 'belt';
  return h('div', {},
    h('div', { class: 'small' }, 'Connected: ', h('b', {}, S.belt.name), ' — its keys control your first 4 channels.'),
    h('label', { for: 'sBeltAudio' }, 'Audio goes through'),
    h('select', { id: 'sBeltAudio', onchange: (e) => { store.set('beltAudio', e.target.value); applyBeltAudio(); openSettings(); } },
      h('option', { value: 'belt', selected: viaBelt }, 'Headset plugged into the beltpack'),
      h('option', { value: 'phone', selected: !viaBelt }, 'This device (AirPods / Bluetooth / wired headphones) — beltpack is a remote')),
    viaBelt ? h('label', {}, h('input', { type: 'checkbox', checked: store.get('beltMonitor', false), onchange: (e) => { store.set('beltMonitor', e.target.checked); S.audio.setMonitor(e.target.checked); } }), ' Also play audio on this device') : null,
    h('button', { onclick: () => S.belt.disconnect() }, 'Disconnect beltpack'));
}

// ---------------- beltpack bridge ----------------
async function connectBelt() {
  try {
    await ensureAudio();
    const belt = new Beltpack({
      onEvent: onBeltEvent,
      onAudio: (pkt) => { // [seq u16][opus] -> uplink frame
        if (!beltCarriesAudio() || S.talk.size === 0 || !S.ws || S.ws.readyState !== 1) return;
        const f = new Uint8Array(pkt.length + 1); f[0] = 1; f.set(pkt, 1); S.ws.send(f);
      },
      onDisconnect: () => {
        S.belt = null; S.beltKeyCache = {}; if (S.beltEnc) { try { S.beltEnc.close(); } catch {} S.beltEnc = null; }
        S.audio.setTap(false); S.audio.setMonitor(true); updateTransmit();
        banner('Beltpack disconnected', 'warn', 4000);
        if (S.sheet?.kind === 'settings') openSettings();
      },
    });
    await belt.connect();
    S.belt = belt; S.beltKeyCache = {};
    applyBeltAudio();
    syncBelt(true);
    if (S.sheet?.kind === 'settings') openSettings();
  } catch (e) {
    if (e && e.name !== 'NotFoundError') banner('Beltpack: ' + e.message, 'bad', 5000);
  }
}

function applyBeltAudio() {
  const belt = S.belt;
  if (!belt) return;
  const viaBelt = beltAudio() === 'belt';
  belt.sendCtrl({ t: 'mode', bridge: 1, audio: viaBelt ? 1 : 0 });
  if (viaBelt) {
    if (!S.beltEnc) {
      S.beltEnc = S.audio.makeEncoder(24000, (bytes) => {
        const p = new Uint8Array(bytes.length + 2); p[0] = S.beltSeq >> 8; p[1] = S.beltSeq & 255; S.beltSeq = (S.beltSeq + 1) & 0xffff;
        p.set(bytes, 2); S.belt && S.belt.sendAudio(p);
      });
    }
    let ts = 0;
    S.audio.onMix = (pcm) => {
      if (!S.beltEnc || S.beltEnc.state !== 'configured') return;
      const ad = new AudioData({ format: 'f32', sampleRate: 48000, numberOfFrames: pcm.length, numberOfChannels: 1, timestamp: ts, data: pcm });
      ts += 20000; S.beltEnc.encode(ad); ad.close();
    };
    S.audio.setTap(true);
    S.audio.setMonitor(store.get('beltMonitor', false));
  } else {
    S.audio.setTap(false);
    S.audio.onMix = null;
    if (S.beltEnc) { try { S.beltEnc.close(); } catch {} S.beltEnc = null; }
    S.audio.setMonitor(true);
  }
  updateTransmit();
}

function onBeltEvent(e) {
  const ch = S.channels[e.k];
  if (e.t === 'hello') return syncBelt(true);
  if (!ch) return;
  if (e.t === 'talk') send({ t: 'talk', ch: ch.id, on: !!e.on });
  else if (e.t === 'listen') setListen(ch.id, !!e.on);
  else if (e.t === 'call') send({ t: 'call', ch: ch.id, on: !!e.on });
  else if (e.t === 'vol') setVol(ch.id, e.v);
}

function syncBelt(force = false) {
  if (!S.belt) return;
  const me = S.welcome?.sid;
  const status = { t: 'st', u: S.welcome?.user || '', on: S.ws?.readyState === 1 ? 1 : 0, m: S.muted ? 1 : 0,
    tl: S.tally === 'program' ? 1 : S.tally === 'preview' ? 2 : 0 };
  const sj = JSON.stringify(status);
  if (force || S.beltKeyCache.st !== sj) { S.beltKeyCache.st = sj; S.belt.sendCtrl(status); }
  for (let k = 0; k < 4; k++) {
    const ch = S.channels[k];
    const st = ch && S.state.get(ch.id);
    const key = ch ? {
      t: 'key', k, n: ch.name.slice(0, 10), c: ch.color.slice(1), ct: ch.canTalk ? 1 : 0, cl: ch.canListen ? 1 : 0,
      tk: S.talk.has(ch.id) ? 1 : 0, ls: S.listen.has(ch.id) ? 1 : 0,
      ac: st && st.talkers.some((t) => t.sid !== me) ? 1 : 0,
      ca: st && st.calls.some((t) => t.sid !== me) ? 1 : 0, v: S.vols[ch.id] ?? 75,
    } : { t: 'key', k, n: '' };
    const kj = JSON.stringify(key);
    if (force || S.beltKeyCache[k] !== kj) { S.beltKeyCache[k] = kj; S.belt.sendCtrl(key); }
  }
}

// ---------------- headphone button as talk toggle ----------------
// Media keys only reach a page that is "playing" media, so a silent stream is
// played through an <audio> element and play/pause actions toggle latch-talk.
function setupMediaPTT() {
  const on = store.get('mediaPTT', false);
  if (!('mediaSession' in navigator) || !S.audio) return;
  if (!on) {
    for (const a of ['play', 'pause', 'stop']) try { navigator.mediaSession.setActionHandler(a, null); } catch {}
    if (S.mediaEl) { S.mediaEl.pause(); S.mediaEl = null; }
    return;
  }
  if (!S.mediaEl) {
    const dest = S.audio.ctx.createMediaStreamDestination();
    const g = S.audio.ctx.createGain(); g.gain.value = 0; g.connect(dest);
    S.mediaEl = new Audio(); S.mediaEl.srcObject = dest.stream; S.mediaEl.play().catch(() => {});
  }
  navigator.mediaSession.metadata = new MediaMetadata({ title: 'OpenLine intercom', artist: S.welcome?.user || '' });
  const toggle = () => {
    if (S.talk.size > 0) { S.latched = false; setTalkTargets(false); }
    else if (S.targets.size && !S.muted) { S.latched = true; setTalkTargets(true); }
    navigator.mediaSession.playbackState = 'playing';
  };
  for (const a of ['play', 'pause', 'stop']) try { navigator.mediaSession.setActionHandler(a, toggle); } catch {}
  navigator.mediaSession.playbackState = 'playing';
}

// ---------------- startup ----------------
async function ensureAudio() {
  if (S.audio) return;
  if (S.audioStarting) return S.audioStarting;
  S.audioStarting = (async () => {
    const a = new AudioEngine();
    a.onUplink = (f) => { if (S.ws && S.ws.readyState === 1 && S.ws.bufferedAmount < 64000) S.ws.send(f); };
    const mic = $('hMic');
    a.onMicLevel = (p) => { mic.style.width = Math.min(100, p * 160).toFixed(0) + '%'; };
    a.onLevels = (lv) => { S.sidLevel = lv; };
    await a.start({ micId: store.get('mic', ''), echoCancel: store.get('aec', true) });
    a.setJitter(store.get('jitter', 60));
    if (store.get('out', '')) a.setOutput(store.get('out', '')).catch(() => {});
    S.audio = a;
    updateTransmit();
    wakeLock();
    setupMediaPTT();
  })();
  try { await S.audioStarting; } finally { S.audioStarting = null; }
}

async function wakeLock() {
  try { if ('wakeLock' in navigator) await navigator.wakeLock.request('screen'); } catch {}
}
// Coming back to the foreground (iOS suspends pages that are swiped away or
// locked): reconnect at once, resume audio, and re-open the mic if iOS ended it.
async function resumeAfterBackground() {
  if (document.visibilityState !== 'visible' || !S.creds) return;
  if (!S.ws || S.ws.readyState > 1) {
    clearTimeout(S.reconnectTimer);
    S.reconnectDelay = 1000;
    connect();
  }
  if (!S.audio) return;
  wakeLock();
  try { if (S.audio.ctx.state !== 'running') await S.audio.ctx.resume(); } catch {}
  const track = S.audio.micStream && S.audio.micStream.getAudioTracks()[0];
  if (!S.belt && store.get('mic', '') !== 'none' && (!track || track.readyState === 'ended' || track.muted)) {
    try { await S.audio.setMic(store.get('mic', ''), store.get('aec', true)); banner('Microphone restarted', 'warn', 2000); }
    catch (e) { banner('Tap anywhere to re-enable the microphone', 'warn'); S.needMic = true; }
  }
}
document.addEventListener('visibilitychange', resumeAfterBackground);
window.addEventListener('pageshow', resumeAfterBackground);
window.addEventListener('focus', resumeAfterBackground);
// iOS may refuse to restart audio without a tap after an interruption (call, Siri…)
document.addEventListener('pointerdown', () => {
  if (!S.audio) return;
  if (S.audio.ctx.state !== 'running') S.audio.ctx.resume().catch(() => {});
  if (S.needMic) { S.needMic = false; resumeAfterBackground(); }
}, true);

function show(id) { for (const v of ['login', 'start', 'app']) $(v).classList.toggle('hidden', v !== id); }
function showLogin(err = '') { show('login'); $('loginErr').textContent = err; }
function showStart() { show('start'); $('startInfo').textContent = `Signed in as ${S.welcome.user} on ${S.welcome.server}.`; }
function showApp() {
  show('app');
  if (!store.get('guideDone', false) && !S.tallyView) setTimeout(() => openGuide(), 400);
}

// ---------------- quick start guide (first run on each device) ----------------
const GUIDE = [
  { title: 'Pick who to talk to', sel: '.ch .tgt:not([disabled])', pos: 'bottom',
    text: 'Tap the coloured square next to a channel. A ✓ means the big button talks to that channel. You can pick several.' },
  { title: 'Talk', sel: '#ptt', pos: 'top',
    text: 'Hold the big button to talk and let go to stop. Quick tap = stays on (latched); tap again to stop. On a keyboard, hold Space.' },
  { title: 'Listen and volume', sel: '.ch .lsn', pos: 'bottom',
    text: 'ON / OFF turns listening to a channel on or off. Tap a channel\'s name for its volume, a call button, and who is connected.' },
  { title: 'Calls and tally', sel: '#callBtn', pos: 'top',
    text: 'Hold CALL to flash the channels you talk to. A flashing yellow row means someone is calling you. A red ON AIR bar means your camera is live.' },
  { title: 'Keep it connected', sel: '#menuBtn', pos: 'bottom',
    text: 'Keep this app open with the screen on (it stays awake by itself). If you switch apps it reconnects when you come back. On iPhone: Share → Add to Home Screen. Menu has your mic, headphones, beltpack and this guide.' },
];

function openGuide(step = 0) {
  closeGuide();
  const g = GUIDE[step];
  const target = document.querySelector(g.sel);
  if (target) { target.classList.add('guide-focus'); target.scrollIntoView({ block: 'nearest' }); }
  const last = step === GUIDE.length - 1;
  const done = () => { store.set('guideDone', true); closeGuide(); };
  document.body.append(
    h('div', { class: 'guide-dim', id: 'guideDim' }),
    h('div', { class: 'guide ' + g.pos, id: 'guide', role: 'dialog', 'aria-label': 'Quick start' },
      h('h2', {}, g.title),
      h('p', {}, g.text),
      h('div', { class: 'steps', 'aria-hidden': 'true' }, GUIDE.map((_, i) => h('i', { class: i === step ? 'on' : '' }))),
      h('div', { class: 'row' },
        h('button', { class: 'ghost', onclick: done }, 'Skip'),
        h('span', { class: 'grow' }),
        step > 0 ? h('button', { class: 'ghost', onclick: () => openGuide(step - 1) }, 'Back') : null,
        h('button', { onclick: () => (last ? done() : openGuide(step + 1)) }, last ? 'Done' : 'Next'))));
}

function closeGuide() {
  for (const id of ['guide', 'guideDim']) $(id)?.remove();
  document.querySelectorAll('.guide-focus').forEach((el) => el.classList.remove('guide-focus'));
}

function logout() {
  store.del('creds'); S.creds = null; S.everWelcomed = false;
  const ws = S.ws; S.ws = null; if (ws) ws.close();
  closeSheet(); showLogin();
}

function bindPress(el, down, up) {
  el.addEventListener('pointerdown', (e) => { e.preventDefault(); el.setPointerCapture?.(e.pointerId); down(); });
  el.addEventListener('pointerup', up);
  el.addEventListener('pointercancel', up);
  el.addEventListener('contextmenu', (e) => e.preventDefault());
}

function init() {
  if (insecureMessage()) {
    showLogin(insecureMessage());
  } else if (!codecSupport()) {
    showLogin('This browser cannot encode Opus audio (WebCodecs). Use a current Chrome, Edge, Firefox or Safari 26+.');
  }
  fetch('api/info').then((r) => r.json()).then((i) => { $('loginTitle').textContent = i.server; }).catch(() => {});
  const hash = new URLSearchParams(location.hash.slice(1));
  if (hash.get('t')) {
    S.creds = { token: hash.get('t') };
    history.replaceState(null, '', location.pathname);
  }
  $('loginForm').addEventListener('submit', (e) => {
    e.preventDefault();
    S.creds = { user: $('lu').value.trim(), pin: $('lp').value };
    S.everWelcomed = false;
    ensureAudio().catch((err) => banner('Audio: ' + err.message, 'bad'));
    connect();
  });
  $('startBtn').addEventListener('click', async () => {
    try { await ensureAudio(); showApp(); renderAll(); }
    catch (err) { $('startErr').textContent = err.message; }
  });
  $('menuBtn').addEventListener('click', openSettings);
  $('clearBtn').addEventListener('click', () => { if (S.talk.size) setTalkTargets(false); S.targets.clear(); store.set('targets', []); renderAll(); });
  bindPress($('ptt'), pttDown, pttUp);
  bindPress($('tvPtt'), pttDown, pttUp);
  $('tvExit').addEventListener('click', () => setTallyView(false));
  bindPress($('callBtn'), () => setCall(true), () => setCall(false));

  document.addEventListener('keydown', (e) => {
    if (e.repeat || e.target.matches('input, select, textarea') || $('app').classList.contains('hidden')) return;
    if (e.code === 'Space') { e.preventDefault(); pttDown(); }
    else if (e.key === 'c' || e.key === 'C') setCall(true);
    else if (/^[1-9]$/.test(e.key)) { const ch = S.channels[+e.key - 1]; if (ch) toggleTarget(ch.id); }
    else if (e.key === 'Escape') closeSheet();
  });
  document.addEventListener('keyup', (e) => {
    if (e.code === 'Space') pttUp();
    else if (e.key === 'c' || e.key === 'C') setCall(false);
  });

  const endSheetCall = () => { if (S.sheetCall != null) { send({ t: 'call', ch: S.sheetCall, on: false }); S.sheetCall = null; } };
  window.addEventListener('pointerup', endSheetCall);
  window.addEventListener('pointercancel', endSheetCall);

  setInterval(() => send({ t: 'ping', ts: performance.now() }), 2000);
  setInterval(() => { S.audio && S.audio.gc(); syncBelt(); }, 1000);
  requestAnimationFrame(levelTick);

  if (S.creds) connect(); else showLogin($('loginErr').textContent);
}

init();
