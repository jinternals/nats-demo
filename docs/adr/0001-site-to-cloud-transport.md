# ADR-0001: Site-to-cloud transport: NATS leaf node, not an HTTP bridge

- **Status:** accepted
- **Date:** 2026-11-18
- **Scope:** how each site exchanges uplink (telemetry, health, command results)
  and downlink (commands) with the cloud

## Context

- 50–500 sites, behind NAT, no inbound ports. Every connection is opened by the site.
- Reliable delivery is the primary requirement: nothing confirmed at the site may
  be lost, through outages and crashes, in either direction.
- Security is per site: one site's compromise must not reach another site, and a
  site must never be able to issue commands.
- Commands expire. A stale command must not run.
- Some sites may only allow outbound HTTPS, possibly through a corporate proxy.

Every site runs a local NATS server with JetStream either way. That local store is
what makes "acked at the site" mean "on the site's disk" (measured:
`scenario-crash.sh`). The decision is only about **the hop between the site and the cloud**.

## Options

### A. NATS leaf node: the site's server dials the hub directly (chosen)

```
edge-app ─► site nats (SITE_UPLINK) ══ leaf, mTLS, tls:// :7422 or wss:// :443 ══► cloud hub
                                                     FLEET_UPLINK ◄─ sources SITE_UPLINK
            site pulls own consumer  ◄────────────── FLEET_COMMANDS
```

### B. HTTP bridge: a service at each site relays to a cloud HTTP API

```
edge-app ─► site nats (SITE_UPLINK) ─► site-bridge ── HTTPS ──► ingest API ─► cloud nats
                                       site-bridge ◄─ HTTPS poll / long-poll ── command API ◄─ cloud nats
```

The cloud cannot call into a site behind NAT, so in B the **site** must also fetch
commands: by polling, long-polling, SSE or WebSocket.

## Comparison

Evidence key: **M** measured in `test/` (nats-server 2.12.1, nats.go 1.46.1) ·
**S** read from nats-server 2.12.1 source · **D** design reasoning (option B was
*not built or measured*).

| Concern | A. Leaf node | B. HTTP bridge |
|---|---|---|
| **Uplink after an outage** | `FLEET_UPLINK` sources each site stream and resumes from its last sequence. 40/40 in order, 0 gaps, through a site `kill -9` and a hub `kill -9` mid-outage (**M**) | The bridge has to do it: read the site stream, batch, POST, retry with backoff, and save its position only after a 2xx. Ordering needs one batch in flight per site. That means re-implementing JetStream sourcing in your own code (**D**) |
| **Duplicates** | Source replay is tracked by stream sequence; publishes carry `Nats-Msg-Id` (**M**/**S**) | Every retry of a batch whose response was lost is a duplicate. You need an idempotency key per message at ingest. Relying on JetStream dedupe would only cover retries inside `duplicate_window` (2 m here), not a retry after a long outage (**D**) |
| **Commands: delivery** | The site pulls its durable consumer over the leaf. Delivered as soon as the link is back, with explicit ack (**M**) | Poll: latency up to the poll interval, plus empty requests from 500 sites. Long-poll/SSE/WebSocket: a custom streaming protocol with its own ack and redelivery rules (**D**) |
| **Commands: expiry** | `Nats-TTL` deletes it in the cloud before it ships (**M**) | The command API must filter expired commands at fetch time; the edge `expiresAt` check stays either way (**D**) |
| **Flow control / backpressure** | Built into sourcing (`$JS.FC`) (**S**) | HTTP 429/503 + `Retry-After`, and a bridge that honours them (**D**) |
| **Per-site identity** | Client cert, `verify_and_map`: the cert subject is the user (**M**) | Client cert or token checked by the API, or by a gateway in front of it (**D**) |
| **Per-site authorization** | Subject permissions on the hub. Cross-site pull refused, command injection denied, unknown cert rejected (**M**) | Written in API code: every endpoint must check the path's site against the caller. One missed check is a cross-site hole (**D**) |
| **HTTPS-only egress, corporate proxy** | Leaf over `wss://` through an HTTP CONNECT proxy, same cert and permissions. 20/20 uplink, command delivered, cross-site refused, unknown cert → `Authorization Violation` (**M**, `scenario-wss.sh`) | Native: plain HTTPS (**D**) |
| **TLS-inspecting proxy** (MITM, re-signs certificates) | Client-cert mTLS can't pass a proxy that terminates TLS, on either transport (**D**) | Same limit for client certs. Token auth survives inspection (**D**) |
| **Silent failure modes** | Hub-side leaf denials are silent (`no responders`, no hub log line), and a mis-permissioned source stalls at 0 with no error (**M**). Monitor source last-seen and leaf DISCONNECT events | The bridge stalls (crash loop, stuck saved position, expired token) while the site keeps acking locally: the same "green dashboards" failure, inside your own code (**D**) |
| **Site-offline signal** | `$SYS.ACCOUNT.FLEET.DISCONNECT` with the cert identity, ~1 min for a black-holed link with ping 20s/2 (**M**) | Heartbeat endpoint plus staleness timer (**D**) |
| **What you run and own** | nats-server at the site and in the cloud; config only (**M**) | Everything in A's site and cloud, **plus** a bridge service per site and an ingest + command API. Code you write, test, version and roll out to 500 sites (**D**) |
| **Contract between site and cloud** | The subject namespace plus three stream/consumer names | An HTTP API: easier to version, document and put behind a gateway (**D**) |
| **Operations tooling** | NATS-specific: `$SYS` events, `nats` CLI, stream info | Standard HTTP: load balancers, WAF, API gateway, access logs (**D**) |

## Decision

**Use option A, the NATS leaf node.** It is the only option where the
reliability properties are already measured rather than promised. Option B doesn't
remove the site JetStream store; it adds a service on top of it that has to
re-derive ordering, resume, deduplication, flow control and per-site authorization
in application code, which is where the MQTT design's silent failures came from.

Transport per site:
- **Default:** `tls://hub:7422`.
- **HTTPS-only egress:** `wss://hub:443`, with `proxy { url: … }` if the site's
  egress uses a CONNECT proxy. Same certificate, same permissions, no code change.

## When option B is the right call instead

- The site's egress proxy **terminates TLS**, so client-certificate mTLS is
  impossible on either transport, and the security team requires a WAF or API
  gateway on every inbound request.
- A **third party owns the site side** and can only integrate over HTTP. Run an
  HTTP ingest for *that* party at the cloud edge; it doesn't change this
  decision for sites you operate.
- Volume is low and commands can tolerate the poll interval.

## Consequences

- Sites need nats-server and a JetStream store (sized: uplink rate × longest
  outage; `sync_interval: always` for power loss: see `production-readiness.md`).
- The team operates NATS, including its silent failure modes (rows 4–5 of
  `production-readiness.md`). Monitoring of source last-seen and leaf events is
  required, not optional.
- The WebSocket listener on 443 and the 7422 leaf port both terminate TLS with
  client-cert verification. A load balancer in front must pass TLS through,
  **not terminate it**, or the per-site identity is lost.

## Verification

| Script | Covers |
|---|---|
| `test/scenario.sh` | outage, uplink catch-up, TTL expiry, cross-site and injection denial |
| `test/scenario-crash.sh` | site and hub `kill -9` mid-outage, leaf connect/disconnect events |
| `test/scenario-wss.sh` | leaf over `wss://` through an HTTP CONNECT proxy, same identity and permissions |

Found while testing the WebSocket path: in a leaf remote, the `proxy { }` block
must come **after** `tls { }`. nats-server 2.12.1 validates it as it parses and
rejects the config ("proxy is configured but remote URL wss://… requires TLS and
no TLS configuration is provided") when `proxy` comes first.
