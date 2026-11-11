// AudioWorklet processors for OpenLine.
//  ol-capture: batches mic input into 20 ms (960 sample) frames + level.
//  ol-player : per-talker adaptive jitter buffers, mixed to one output.

const FRAME = 960; // 20 ms @ 48 kHz

class Capture extends AudioWorkletProcessor {
  constructor() {
    super();
    this.buf = new Float32Array(FRAME);
    this.n = 0;
    this.peak = 0;
  }
  process(inputs) {
    const ch = inputs[0] && inputs[0][0];
    if (!ch) return true;
    for (let i = 0; i < ch.length; i++) {
      const v = ch[i];
      this.buf[this.n++] = v;
      const a = v < 0 ? -v : v;
      if (a > this.peak) this.peak = a;
      if (this.n === FRAME) {
        this.port.postMessage({ pcm: this.buf, peak: this.peak }, [this.buf.buffer]);
        this.buf = new Float32Array(FRAME);
        this.n = 0;
        this.peak = 0;
      }
    }
    return true;
  }
}

class Player extends AudioWorkletProcessor {
  constructor() {
    super();
    this.src = new Map(); // sid -> source
    this.baseTarget = Math.round(0.06 * sampleRate); // 60 ms initial jitter buffer
    this.maxTarget = Math.round(0.30 * sampleRate);
    this.tap = false; // post mixed frames to main thread (beltpack bridge)
    this.tapBuf = new Float32Array(FRAME);
    this.tapN = 0;
    this.tapTail = 0;
    this.lvlTick = 0;
    this.port.onmessage = (e) => this.onMsg(e.data);
  }

  onMsg(m) {
    if (m.pcm) {
      let s = this.src.get(m.sid);
      if (!s) {
        s = { q: [], off: 0, avail: 0, primed: false, gain: m.gain, cur: m.gain,
              target: this.baseTarget, peak: 0, idle: 0, stable: 0 };
        this.src.set(m.sid, s);
      }
      s.q.push(m.pcm);
      s.avail += m.pcm.length;
      s.gain = m.gain;
      s.idle = 0;
      // Too much buffered (burst after a network stall): drop oldest to cap latency.
      while (s.avail > s.target + this.maxTarget) this.dropFrame(s);
    } else if (m.gain !== undefined && m.sid !== undefined) {
      const s = this.src.get(m.sid);
      if (s) s.gain = m.gain;
    } else if (m.tap !== undefined) {
      this.tap = m.tap;
    } else if (m.jitterMs !== undefined) {
      this.baseTarget = Math.round(m.jitterMs / 1000 * sampleRate);
    }
  }

  dropFrame(s) {
    const f = s.q.shift();
    if (!f) return;
    s.avail -= f.length - s.off;
    s.off = 0;
  }

  process(_inputs, outputs) {
    const out = outputs[0][0];
    const n = out.length;
    out.fill(0);
    let active = false;
    for (const [sid, s] of this.src) {
      if (!s.primed) {
        if (s.avail >= s.target) s.primed = true;
        else {
          if (++s.idle > 3000) this.src.delete(sid); // ~8 s silent: forget
          continue;
        }
      }
      active = true;
      // gain ramp across the block to avoid zipper noise
      const g0 = s.cur, g1 = s.gain, dg = (g1 - g0) / n;
      let g = g0, peak = 0, i = 0;
      while (i < n) {
        const f = s.q[0];
        if (!f) break;
        const take = Math.min(n - i, f.length - s.off);
        for (let k = 0; k < take; k++) {
          const v = f[s.off + k];
          const a = v < 0 ? -v : v;
          if (a > peak) peak = a;
          out[i + k] += v * g;
          g += dg;
        }
        i += take;
        s.off += take;
        s.avail -= take;
        if (s.off >= f.length) { s.q.shift(); s.off = 0; }
      }
      s.cur = g1;
      if (peak > s.peak) s.peak = peak;
      if (i < n) {
        // underrun: talker stopped, or network jitter. Grow buffer if mid-stream.
        s.primed = false;
        if (s.stable > 50) s.target = this.baseTarget; // long clean run before: likely just end of talk
        else s.target = Math.min(s.target + Math.round(0.02 * sampleRate), this.maxTarget);
        s.stable = 0;
      } else if (++s.stable > 1500 && s.target > this.baseTarget) {
        // ~4 s without underrun: shrink back toward base
        s.target -= Math.round(0.01 * sampleRate);
        s.stable = 1000;
        if (s.avail > s.target + 960) this.dropFrame(s);
      }
    }
    // soft clip
    for (let i = 0; i < n; i++) {
      const v = out[i];
      if (v > 1 || v < -1) out[i] = Math.tanh(v);
    }

    if (this.tap) {
      if (active) this.tapTail = 10; // keep sending 200 ms after activity
      if (this.tapTail > 0) {
        for (let i = 0; i < n; i++) {
          this.tapBuf[this.tapN++] = out[i];
          if (this.tapN === FRAME) {
            this.port.postMessage({ mix: this.tapBuf }, [this.tapBuf.buffer]);
            this.tapBuf = new Float32Array(FRAME);
            this.tapN = 0;
            if (!active) this.tapTail--;
          }
        }
      }
    }

    if (++this.lvlTick >= 15) { // ~40 ms
      this.lvlTick = 0;
      const levels = {};
      for (const [sid, s] of this.src) {
        levels[sid] = s.peak;
        s.peak = 0;
      }
      this.port.postMessage({ levels });
    }
    return true;
  }
}

registerProcessor('ol-capture', Capture);
registerProcessor('ol-player', Player);
