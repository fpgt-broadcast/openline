// Audio engine: mic capture -> Opus (WebCodecs) uplink, and
// per-talker Opus decode -> jitter-buffered mixer -> speakers.

const SR = 48000;
const FRAME = 960;

export function codecSupport() {
  return typeof AudioEncoder !== 'undefined' && typeof AudioDecoder !== 'undefined';
}

export class AudioEngine {
  constructor() {
    this.ctx = null;
    this.transmit = false;      // send local mic
    this.onUplink = null;       // (Uint8Array frame) => void   (proto uplink frame)
    this.onMicLevel = null;     // (peak) => void
    this.onLevels = null;       // ({sid: peak}) => void
    this.onMix = null;          // (Float32Array 960) => void   (tap for beltpack)
    this.decoders = new Map();  // sid -> {dec, ts, gain, last}
    this.seq = 0;
    this.encTs = 0;
    this.micStream = null;
  }

  async start({ micId, echoCancel = true } = {}) {
    if (!codecSupport()) throw new Error('This browser lacks WebCodecs audio (use current Chrome, Edge, Firefox or Safari 26+).');
    this.ctx = new AudioContext({ sampleRate: SR, latencyHint: 'interactive' });
    await this.ctx.audioWorklet.addModule('worklets.js');

    this.player = new AudioWorkletNode(this.ctx, 'ol-player', { numberOfInputs: 0, outputChannelCount: [1] });
    this.outGain = this.ctx.createGain();
    this.player.connect(this.outGain).connect(this.ctx.destination);
    this.player.port.onmessage = (e) => {
      const m = e.data;
      if (m.levels && this.onLevels) this.onLevels(m.levels);
      else if (m.mix && this.onMix) this.onMix(m.mix);
    };

    this.capture = new AudioWorkletNode(this.ctx, 'ol-capture', { numberOfOutputs: 1, outputChannelCount: [1] });
    const sink = this.ctx.createGain();
    sink.gain.value = 0;
    this.capture.connect(sink).connect(this.ctx.destination); // keep node pulled
    this.capture.port.onmessage = (e) => this.onMicFrame(e.data);

    this.enc = this.makeEncoder(32000, (bytes) => {
      const f = new Uint8Array(3 + bytes.length);
      f[0] = 1;
      f[1] = this.seq >> 8; f[2] = this.seq & 255;
      this.seq = (this.seq + 1) & 0xffff;
      f.set(bytes, 3);
      if (this.onUplink) this.onUplink(f);
    });
    await this.setMic(micId, echoCancel);
    if (this.ctx.state !== 'running') await this.ctx.resume();
  }

  makeEncoder(bitrate, onBytes) {
    const enc = new AudioEncoder({
      output: (chunk) => {
        const b = new Uint8Array(chunk.byteLength);
        chunk.copyTo(b);
        onBytes(b);
      },
      error: (e) => console.error('encoder', e),
    });
    enc.configure({
      codec: 'opus', sampleRate: SR, numberOfChannels: 1, bitrate,
      opus: { frameDuration: 20000, complexity: 5 },
    });
    return enc;
  }

  async setMic(micId, echoCancel = true) {
    if (this.micSrc) { this.micSrc.disconnect(); this.micSrc = null; }
    if (this.micStream) this.micStream.getTracks().forEach((t) => t.stop());
    this.micStream = null;
    if (micId === 'none') return;
    this.micStream = await navigator.mediaDevices.getUserMedia({
      audio: {
        deviceId: micId ? { exact: micId } : undefined,
        channelCount: 1,
        echoCancellation: echoCancel,
        noiseSuppression: true,
        autoGainControl: true,
      },
    });
    this.micSrc = this.ctx.createMediaStreamSource(this.micStream);
    this.micSrc.connect(this.capture);
  }

  async setOutput(deviceId) {
    if (this.ctx && this.ctx.setSinkId) await this.ctx.setSinkId(deviceId || '');
  }

  setMonitor(on) { if (this.outGain) this.outGain.gain.value = on ? 1 : 0; }
  setTap(on) { this.player && this.player.port.postMessage({ tap: on }); }
  setJitter(ms) { this.player && this.player.port.postMessage({ jitterMs: ms }); }

  onMicFrame({ pcm, peak }) {
    if (this.onMicLevel) this.onMicLevel(peak);
    if (!this.transmit || this.enc.state !== 'configured') return;
    const ad = new AudioData({ format: 'f32', sampleRate: SR, numberOfFrames: FRAME, numberOfChannels: 1, timestamp: this.encTs, data: pcm });
    this.encTs += 20000;
    this.enc.encode(ad);
    ad.close();
  }

  // Incoming server downlink frame: [0x02][sid u16][seq u16][mask u32][opus]
  // gainFor(mask) returns linear gain (0 = muted)
  receive(buf, gainFor) {
    const dv = new DataView(buf);
    if (dv.byteLength < 10 || dv.getUint8(0) !== 2) return null;
    const sid = dv.getUint16(1), mask = dv.getUint32(5);
    let d = this.decoders.get(sid);
    if (!d) {
      d = { ts: 0, gain: 1, last: 0 };
      d.dec = new AudioDecoder({
        output: (ad) => {
          const pcm = new Float32Array(ad.numberOfFrames);
          ad.copyTo(pcm, { planeIndex: 0, format: 'f32-planar' });
          ad.close();
          this.player.port.postMessage({ sid, pcm, gain: d.gain }, [pcm.buffer]);
        },
        error: (e) => { console.warn('decoder', sid, e); this.decoders.delete(sid); },
      });
      d.dec.configure({ codec: 'opus', sampleRate: SR, numberOfChannels: 1 });
      this.decoders.set(sid, d);
    }
    d.gain = gainFor(mask);
    d.last = performance.now();
    if (d.dec.state !== 'configured') return { sid, mask };
    d.dec.decode(new EncodedAudioChunk({ type: 'key', timestamp: d.ts, data: new Uint8Array(buf, 9) }));
    d.ts += 20000;
    return { sid, mask };
  }

  // Re-apply gains to everything currently playing (volume knob moved).
  regain(gainForSid) {
    for (const [sid, d] of this.decoders) {
      d.gain = gainForSid(sid);
      this.player.port.postMessage({ sid, gain: d.gain });
    }
  }

  gc() {
    const now = performance.now();
    for (const [sid, d] of this.decoders) {
      if (now - d.last > 10000) {
        try { d.dec.close(); } catch {}
        this.decoders.delete(sid);
      }
    }
  }

  async stop() {
    for (const d of this.decoders.values()) try { d.dec.close(); } catch {}
    this.decoders.clear();
    if (this.micStream) this.micStream.getTracks().forEach((t) => t.stop());
    try { this.enc && this.enc.close(); } catch {}
    if (this.ctx) await this.ctx.close();
    this.ctx = null;
  }
}
