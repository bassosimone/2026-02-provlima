// SPDX-License-Identifier: AGPL-3.0-or-later

/**
 * NDT8 JavaScript client.
 *
 * Implements the ndt8 measurement protocol: session-based chunk-doubling
 * HTTP transfers with concurrent responsiveness probes.
 *
 * I/O strategy choices based on js-perf benchmarks:
 * - Download: XHR+Blob (8.8-9.4 Gbit/s on localhost, progress via onprogress)
 * - Upload: Fetch+Blob (10.5-13.6 Gbit/s server-measured, simple API)
 * - Probes: Fetch (lightweight GET, RTT via performance.now())
 */
export class NDT8Client {
  static INITIAL_CHUNK_SIZE = 32;
  static MAX_CHUNK_SIZE = 256 << 20; // 256 MiB
  static TIME_BUDGET_MS = 10_000;    // 10 seconds per direction
  static PROBE_INTERVAL_MS = 250;    // 250ms between probes

  #baseURL;
  #sessionID = null;
  #onEvent;

  /**
   * @param {string} baseURL - Server origin (e.g. "https://127.0.0.1:4443")
   * @param {object} [options]
   * @param {function} [options.onEvent] - Callback receiving {type, ...} event objects
   */
  constructor(baseURL, { onEvent = () => {} } = {}) {
    this.#baseURL = baseURL.replace(/\/$/, '');
    this.#onEvent = onEvent;
  }

  /** Run the full measurement: create session, download, upload, delete. */
  async run() {
    await this.#createSession();
    try {
      this.#emit('download:start');
      await this.#runWithProbes('download');
      this.#emit('download:done');

      this.#emit('upload:start');
      await this.#runWithProbes('upload');
      this.#emit('upload:done');
    } finally {
      await this.#deleteSession();
    }
    this.#emit('complete');
  }

  // -- Session lifecycle ---------------------------------------------------

  async #createSession() {
    const resp = await fetch(`${this.#baseURL}/ndt/v8/session`, { method: 'POST' });
    if (!resp.ok) throw new Error(`create session: HTTP ${resp.status}`);
    const { sessionID } = await resp.json();
    this.#sessionID = sessionID;
    this.#emit('session:created', { sessionID });
  }

  async #deleteSession() {
    try {
      const url = `${this.#baseURL}/ndt/v8/session/${this.#sessionID}`;
      const resp = await fetch(url, { method: 'DELETE' });
      this.#emit('session:deleted', { sessionID: this.#sessionID, status: resp.status });
    } catch (err) {
      this.#emit('session:delete-failed', { error: err.message });
    }
    this.#sessionID = null;
  }

  // -- Chunk-doubling with concurrent probes -------------------------------

  async #runWithProbes(direction) {
    const controller = new AbortController();
    const probesDone = this.#runProbes(controller.signal);

    const t0 = performance.now();
    for (let size = NDT8Client.INITIAL_CHUNK_SIZE; size <= NDT8Client.MAX_CHUNK_SIZE; size *= 2) {
      if (performance.now() - t0 >= NDT8Client.TIME_BUDGET_MS) break;
      try {
        if (direction === 'download') {
          await this.#downloadChunk(size);
        } else {
          await this.#uploadChunk(size);
        }
      } catch (err) {
        this.#emit(`${direction}:error`, { size, error: err.message });
        break;
      }
    }

    controller.abort();
    await probesDone;
  }

  // -- Download: XHR + Blob ------------------------------------------------

  #downloadChunk(size) {
    return new Promise((resolve, reject) => {
      const url = `${this.#baseURL}/ndt/v8/session/${this.#sessionID}/chunk/${size}`;
      const xhr = new XMLHttpRequest();
      xhr.open('GET', url);
      xhr.responseType = 'blob';

      const t0 = performance.now();
      let lastEmit = t0;

      xhr.onprogress = (ev) => {
        const now = performance.now();
        if (now - lastEmit >= 250) {
          this.#emit('download:progress', {
            size,
            bytes: ev.loaded,
            elapsed: now - t0,
            speed: this.#speed(ev.loaded, now - t0),
          });
          lastEmit = now;
        }
      };

      xhr.onload = () => {
        const elapsed = performance.now() - t0;
        const bytes = xhr.response.size;
        this.#emit('download:chunk', {
          size,
          bytes,
          elapsed,
          speed: this.#speed(bytes, elapsed),
        });
        resolve();
      };

      xhr.onerror = () => reject(new Error(`download chunk ${size}: network error`));
      xhr.send();
    });
  }

  // -- Upload: Fetch + Blob ------------------------------------------------

  async #uploadChunk(size) {
    const url = `${this.#baseURL}/ndt/v8/session/${this.#sessionID}/chunk/${size}`;
    const blob = this.#makeBlob(size);

    const t0 = performance.now();
    const resp = await fetch(url, { method: 'PUT', body: blob });
    const elapsed = performance.now() - t0;

    this.#emit('upload:chunk', {
      size,
      bytes: size,
      elapsed,
      speed: this.#speed(size, elapsed),
      status: resp.status,
    });
  }

  /** Construct a Blob of exact size referencing a shared 1 MiB buffer. */
  #makeBlob(size) {
    const CHUNK = 1 << 20; // 1 MiB
    if (size <= CHUNK) {
      return new Blob([new Uint8Array(size)]);
    }
    const buffer = new Uint8Array(CHUNK);
    const parts = [];
    let remaining = size;
    while (remaining > 0) {
      const n = Math.min(remaining, CHUNK);
      parts.push(n === CHUNK ? buffer : buffer.subarray(0, n));
      remaining -= n;
    }
    return new Blob(parts);
  }

  // -- Probes --------------------------------------------------------------

  async #runProbes(signal) {
    while (!signal.aborted) {
      const pid = crypto.randomUUID();
      const t0 = performance.now();
      try {
        const url = `${this.#baseURL}/ndt/v8/session/${this.#sessionID}/probe/${pid}`;
        await fetch(url, { signal });
        this.#emit('probe', { pid, rtt: performance.now() - t0 });
      } catch {
        if (signal.aborted) break;
      }
      await this.#sleep(NDT8Client.PROBE_INTERVAL_MS, signal);
    }
  }

  // -- Helpers -------------------------------------------------------------

  #sleep(ms, signal) {
    return new Promise((resolve) => {
      if (signal.aborted) { resolve(); return; }
      const timer = setTimeout(resolve, ms);
      signal.addEventListener('abort', () => {
        clearTimeout(timer);
        resolve();
      }, { once: true });
    });
  }

  #speed(bytes, elapsedMs) {
    const seconds = elapsedMs / 1000;
    return seconds > 0 ? (bytes * 8) / seconds : 0;
  }

  #emit(type, data = {}) {
    this.#onEvent({ type, ...data });
  }
}
