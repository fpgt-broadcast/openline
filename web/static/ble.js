// Web Bluetooth link to an OpenLine beltpack (BLE bridge mode) and BLE
// provisioning for standalone Wi-Fi / Ethernet mode.
// GATT layout must match firmware/main/ble_link.c.

const BASE = (n) => `4f4c${n}-8e2b-4d6e-9c1a-5b3f2a7d1e60`;
export const UUID = {
  service: BASE('0001'),
  ctrlRx: BASE('0002'),   // app -> device JSON (write)
  ctrlTx: BASE('0003'),   // device -> app JSON (notify)
  audioUp: BASE('0004'),  // device -> app [seq u16][opus] (notify)
  audioDn: BASE('0005'),  // app -> device [seq u16][opus] (write without response)
  prov: BASE('0006'),     // provisioning JSON (write)
  info: BASE('0007'),     // device info JSON (read)
};

export function bleSupported() { return !!(navigator.bluetooth && navigator.bluetooth.requestDevice); }

const enc = new TextEncoder();
const dec = new TextDecoder();

async function pick() {
  const dev = await navigator.bluetooth.requestDevice({ filters: [{ services: [UUID.service] }], optionalServices: [UUID.service] });
  const gatt = await dev.gatt.connect();
  const svc = await gatt.getPrimaryService(UUID.service);
  return { dev, gatt, svc };
}

export class Beltpack {
  constructor({ onEvent, onAudio, onDisconnect }) {
    this.onEvent = onEvent; this.onAudio = onAudio; this.onDisconnect = onDisconnect;
    this.ctrlQ = Promise.resolve();
    this.audioBusy = false; this.audioNext = null;
  }

  async connect() {
    const { dev, svc } = await pick();
    this.dev = dev;
    this.name = dev.name || 'Beltpack';
    dev.addEventListener('gattserverdisconnected', () => this.onDisconnect && this.onDisconnect());
    this.ctrlRx = await svc.getCharacteristic(UUID.ctrlRx);
    this.audioDn = await svc.getCharacteristic(UUID.audioDn);
    const tx = await svc.getCharacteristic(UUID.ctrlTx);
    tx.addEventListener('characteristicvaluechanged', (e) => {
      try { this.onEvent(JSON.parse(dec.decode(e.target.value))); } catch {}
    });
    await tx.startNotifications();
    const up = await svc.getCharacteristic(UUID.audioUp);
    up.addEventListener('characteristicvaluechanged', (e) => {
      const v = e.target.value;
      this.onAudio(new Uint8Array(v.buffer, v.byteOffset, v.byteLength).slice());
    });
    await up.startNotifications();
    await this.sendCtrl({ t: 'mode', bridge: 1 });
  }

  // Control messages are small JSON objects; writes are serialized.
  sendCtrl(obj) {
    const data = enc.encode(JSON.stringify(obj));
    this.ctrlQ = this.ctrlQ.then(() => this.ctrlRx.writeValueWithResponse(data)).catch(() => {});
    return this.ctrlQ;
  }

  // Audio writes: never queue more than one packet behind the in-flight one
  // (latency beats completeness for intercom).
  sendAudio(pkt) {
    if (this.audioBusy) { this.audioNext = pkt; return; }
    this.audioBusy = true;
    this.audioDn.writeValueWithoutResponse(pkt).catch(() => {}).finally(() => {
      this.audioBusy = false;
      const n = this.audioNext; this.audioNext = null;
      if (n) this.sendAudio(n);
    });
  }

  disconnect() { try { this.dev.gatt.disconnect(); } catch {} }
}

// Provision a beltpack for standalone network mode. cfg keys:
// mode (wifi|ethernet|ble), ssid, pass, server, token, dhcp, ip, mask, gw, dns, name
// Each key is written separately to stay under the BLE MTU, then committed.
export async function provision(cfg, onProgress = () => {}) {
  const { dev, svc } = await pick();
  try {
    const info = await svc.getCharacteristic(UUID.info).then((c) => c.readValue()).then((v) => JSON.parse(dec.decode(v))).catch(() => ({}));
    const prov = await svc.getCharacteristic(UUID.prov);
    const entries = Object.entries(cfg).filter(([, v]) => v !== undefined && v !== null);
    let i = 0;
    for (const [k, v] of entries) {
      onProgress(`writing ${k} (${++i}/${entries.length})`);
      await prov.writeValueWithResponse(enc.encode(JSON.stringify({ k, v })));
    }
    onProgress('saving & rebooting');
    await prov.writeValueWithResponse(enc.encode(JSON.stringify({ commit: 1 })));
    return info;
  } finally {
    setTimeout(() => { try { dev.gatt.disconnect(); } catch {} }, 500);
  }
}
