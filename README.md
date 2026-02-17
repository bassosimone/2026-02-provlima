# 2026-02-provlima

A simplified ndt8 prototype — an HTTP-based network measurement protocol
intended to replace [ndt7](https://github.com/m-lab/ndt-server). This
repository presents the design, shows what works well, and documents the
problems identified during prototyping.

This is an **initial design with known issues**. The prototype came first;
the investigations into those issues came after.

## Protocol design

The protocol uses standard HTTP semantics — no WebSocket, no custom
binary framing. A measurement session looks like this:

```
Client                                          Server
  |                                               |
  |  POST /ndt/v8/session                         |
  |---------------------------------------------->|
  |                          201 { sessionID }    |
  |<----------------------------------------------|
  |                                               |
  |  GET /ndt/v8/session/{sid}/chunk/{size}       |
  |---------------------------------------------->|  (download: server streams
  |                  200 <zero bytes streaming>   |   data, both sides sample
  |<----------------------------------------------|   throughput periodically)
  |                                               |
  |  ... repeat with doubling sizes ...           |
  |                                               |
  |  PUT /ndt/v8/session/{sid}/chunk/{size}       |
  |---------------------------------------------->|  (upload: client streams
  |                                      204      |   data, both sides sample)
  |<----------------------------------------------|
  |  ... repeat with doubling sizes ...           |
  |                                               |
  |  GET /ndt/v8/session/{sid}/probe/{pid}        |
  |---------------------------------------------->|  (responsiveness: small
  |                                      204      |   request during transfers
  |<----------------------------------------------|   to measure RTT under
  |  ... concurrent with transfers ...            |   load)
  |                                               |
  |  (result exchange not implemented             |
  |   in this prototype)                          |
```

### Chunk doubling

Transfers start small (32 bytes) and double the chunk size on each
iteration up to 256 MiB, with a total time budget of ~10 seconds per
direction. This progressive approach means:

- Small chunks complete quickly, providing early RTT and TTFB samples.
- Large chunks saturate the link, measuring sustained throughput.
- The transition from small to large naturally captures the relationship
  between transfer size and achievable speed.

### Responsiveness probes

During transfers, the client sends small GET requests to a `/probe`
endpoint. The RTT of these requests reflects network responsiveness
under load — this is how bufferbloat is detected. When the network
is well-managed, probe RTT stays close to the base RTT. When buffers
are bloated, probe RTT increases dramatically.

### Logging

Both client and server emit structured logs (JSON) to stderr. This
prototype does not implement a result exchange mechanism between
client and server — each side logs its own observations independently.

## What works well

1. **Standard HTTP semantics.** GET for download, PUT for upload, POST
   for session management. No WebSocket upgrade, no custom framing.
   Works with any HTTP library, any CDN, any proxy. The protocol is
   debuggable with curl.

2. **Protocol version flexibility.** The same API works over HTTP/1.1,
   HTTP/2, and HTTP/3. The client and server negotiate the best available
   version via ALPN. This is a significant advantage over ndt7, which is
   tied to WebSocket (and therefore HTTP/1.1 for the upgrade handshake).

3. **Chunk doubling captures the full picture.** Small transfers measure
   latency and connection setup cost. Large transfers measure throughput.
   The progression from one to the other shows how performance scales
   with transfer size — something ndt7's fixed-duration approach misses.

4. **Dual-perspective measurement.** Both client and server independently
   measure the same transfers. Comparing perspectives reveals measurement
   artifacts (e.g., browser upload timing includes blob serialization
   overhead — see [2026-02-js-perf](https://github.com/bassosimone/2026-02-js-perf)
   for details).

## Issues identified

### Continuity with ndt7

For ndt8 to be a credible successor to ndt7 (which itself continued the
ndt5 and ndt4 measurement tradition), we need confidence that at least
one of the chunk-doubling transfers approximates what ndt7 does today:
roughly the same duration (conditioned on a maximum byte count) and
comparable throughput — at least in terms of software bottlenecks, as
opposed to the network being the limiting factor.

This requirement motivated two investigations:

**Server-side HTTP/2 throughput.** The initial prototype used Go's
`x/net/http2` for the server. Testing revealed that Go's HTTP/2
implementation is a throughput bottleneck: 4-7 Gbit/s vs 20 Gbit/s
for HTTP/1.1+TLS over the same connection. A Rust HTTP/2 server
(using hyper/h2) closes the gap. Investigated in detail in
[2026-02-http2-perf](https://github.com/bassosimone/2026-02-http2-perf).

**Browser JavaScript client performance.** The real-world ndt8 client
will be JavaScript running in a browser. Browser network stacks sit in
a separate process from the renderer where JavaScript runs. Data must
cross an IPC boundary to reach JS, capping throughput at 6-9 Gbit/s
regardless of protocol. Despite this ceiling, HTTP/2 with a Rust server
outperforms ndt7 WebSocket from the browser (9.4 vs 6 Gbit/s download).
Investigated in detail in
[2026-02-js-perf](https://github.com/bassosimone/2026-02-js-perf).

### The same-origin problem for responsiveness (open)

This problem emerged when trying to understand the probe results the
prototype was producing.

The responsiveness probes are the most valuable part of this design —
and also the most problematic.

The problem: **probe requests share a connection with data transfers.**

**HTTP/1.1.** Browsers enforce per-origin connection limits (typically
6 in Firefox and Chrome). During a large download or upload, most or
all connections are occupied by data transfer. A probe request either:

- **Queues** behind the data transfer on the same connection, measuring
  head-of-line blocking rather than network RTT.
- **Opens a new connection** (if under the limit), but the TCP + TLS
  handshake cost dominates the measurement.
- **Gets blocked entirely** if all connection slots are in use.

None of these outcomes produce a valid RTT measurement under load.

**HTTP/2 and HTTP/3.** The situation is worse.
[RFC 9113](https://www.rfc-editor.org/rfc/rfc9113) (HTTP/2) and
[RFC 9114](https://www.rfc-editor.org/rfc/rfc9114) (HTTP/3) strongly
encourage clients to use a **single connection per origin**. Browsers
follow this guidance. This means:

- Probe requests and data transfers multiplex as streams on the **same
  TCP (or QUIC) connection**.
- Probe stream latency is affected by the data stream's flow control
  window, congestion window, and send buffer occupancy.
- You are measuring **transport-layer scheduling**, not network
  responsiveness.

The probe RTT under HTTP/2 reflects how the h2 implementation
prioritizes streams, not whether the network path has bloated buffers.

**Possible mitigations.** Two directions have been identified but not
yet implemented:

1. **Two-origin architecture.** Place the data server and the probe
   endpoint on different origins (different hostnames or ports). This
   forces the browser to open separate TCP/QUIC connections for probes
   vs data, producing genuine RTT measurements. The cost: more complex
   deployment, DNS/TLS setup for two origins, and CORS configuration.

2. **Separate transport for probes.** Use a non-HTTP channel for
   responsiveness probes — e.g., a WebSocket connection, a WebRTC
   data channel, or WebTransport. Each runs over its own transport,
   independent of the HTTP connection used for data transfer. The
   cost: additional protocol complexity and the fact that the probe
   path may differ from the data path.

Both approaches complicate the protocol. This is an open design
question.

### Deliberately omitted optimizations

This prototype does not include several optimizations present in ndt7,
notably Poisson-based throughput sampling and BBR congestion control
tuning. These are intentionally left out to keep the prototype simple
and focused on the protocol design questions above.

## Setup

Build the two binaries:

```
go build -v ./cmd/gencert
go build -v ./cmd/ndt8
```

Generate a self-signed TLS certificate (reuses existing certs if still
valid and matching the requested IP):

```
./gencert --ip-addr 127.0.0.1
```

This writes `testdata/cert.pem` and `testdata/key.pem`.

Start the server:

```
./ndt8 serve
```

By default, the server listens on `127.0.0.1:4443`, serves the API
endpoints, and serves the browser client from `./static/`. Use
`./ndt8 serve -h` for options (`-A`, `-p`, `--cert`, `--key`, `-s`).

Run a measurement with the Go client:

```
./ndt8 measure
```

This runs download and upload with concurrent probes against the local
server. Use `./ndt8 measure -h` for options (`-A`, `-p`, `--cert`, `-2`
for HTTP/2).

Run a measurement from the browser: open `https://127.0.0.1:4443/` and
click "Run Test". You will need to accept the self-signed certificate.

## Network emulation

*TODO: LXC container setup, netem profiles (2g through ftth-1g, with
bufferbloat variants), lxs launcher.*

## Related work

- [ndt7](https://github.com/m-lab/ndt-server) — the current M-Lab
  network measurement protocol, using WebSocket.
- [2026-02-http2-perf](https://github.com/bassosimone/2026-02-http2-perf) —
  server-side HTTP/2 benchmarks (Go vs Rust).
- [2026-02-js-perf](https://github.com/bassosimone/2026-02-js-perf) —
  JavaScript browser client performance across protocols.
