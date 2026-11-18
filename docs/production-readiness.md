# Production readiness: the MQTT design's failure modes, replayed against NATS

Each row is a problem documented in [`jinternals/mqtt`](https://github.com/jinternals/mqtt)
(README, FEATURES.md, docs/mqtt5-bridge-durability.md), checked against this design.
"Measured" means it ran in `test/` on nats-server v2.12.1 / nats.go v1.46.1.
"Source" means it was read from nats-server v2.12.1 code.

## Problems the MQTT design had to work around

| # | MQTT problem | NATS design | Evidence |
|---|---|---|---|
| 1 | **Session lost on reconnect → commands silently discarded.** In MQTT 5, Session Expiry defaults to 0 and mosquitto's bridge never sends it, so the bridge had to stay on 3.1.1 | **Does not apply.** Commands sit in a stream; the durable consumer has no connection-bound session and no `inactive_threshold`, so it survives any outage length | Measured: command queued mid-outage, hub `kill -9` + restart, delivered after restore |
| 2 | **Outbound spool loses data** (NanoMQ: 5/5 lost, `-full` image segfaults) | **Does not apply.** The site JetStream stream is the spool; the edge publish ack comes from site disk | Measured: 30 msgs during outage, site server `kill -9` mid-outage, 40/40 in cloud, in order, 0 gaps |
| 3 | **Message expiry doesn't cross the 3.1.1 hop**, so stale commands relied on the payload `expiresAt` | **Fixed at the broker.** Per-message `Nats-TTL` deletes the command in the cloud before it ships. Keep `expiresAt` at the edge (time spent queued *at* the site) | Measured: 5 s TTL command never delivered; 15 m one delivered |
| 4 | **Denied publish returns PUBACK 0** on the 3.1.1 hop; denied subscribe is SUBACK'd and silently never fed | **Partly better.** A client on a site server gets an explicit `Permissions Violation` and it is logged. But a request that falls outside the site's **hub account** (not imported) is silent: the requester sees `no responders`, and the hub logs nothing | Measured: `edge-inject` → explicit error; `ops-steal` → `no responders`, no hub log line |
| 5 | **Asymmetric silent failure**: telemetry fine, commands vanish, dashboards green | **Still possible, in a different place.** With wrong leaf permissions (first draft), `FLEET_UPLINK` sourcing received 0 msgs with **no error logged**. A missing import/export in the per-site accounts fails the same way. And `lag` reads 0 during an outage | Measured. Generate accounts with `gen-hub-accounts.sh`, never by hand; alert on source `last_seen` (Active), not `lag` |
| 6 | `bridge/state` notification is the WAN-health signal | **Built in, with identity.** The hub emits `$SYS.ACCOUNT.SITEn.{CONNECT,DISCONNECT}` (the site's own account) with `kind=Leafnode user=CN=leaf-siteN,O=fleet` | Measured |
| 7 | **Black-holed link** (socket alive, nothing flows): mosquitto keepalive 20 s | **Default is slow.** `DEFAULT_PING_INTERVAL = 2m`, `PING_MAX_OUT = 2`, so minutes of a dead link that looks up. Set `ping_interval: 20s`, `ping_max: 2` on hub and sites | Source + measured: frozen proxy, link dropped after 61 s |
| 8 | **Reconnect stampede**: `restart_timeout` jitter so 200 sites don't reconnect in lockstep | **Built in.** Delay = reconnect + random(0, reconnect). Set `reconnect: 5s` | Source: server/leafnode.go |
| 9 | **Crash/power durability**: mosquitto `autosave_interval 30`, so up to 30 s of in-memory state | **Better on crash, the same class of risk on power loss.** Every write reaches the OS at once (server `kill -9` lost nothing), but fsync runs every **2 m** by default. Configs now set `sync_interval: always` on sites | Source: `defaultSyncInterval = 2 * time.Minute`; crash measured. Power loss **not** testable here |
| 10 | **Duplicates** (QoS 1 + bridge replay) | **Same: at-least-once.** Keep `commandId` dedupe at the edge. `Nats-Msg-Id` dedupes publishes within `duplicate_window` | Design |
| 11 | **Reordering / replay** after a backlog drain | **Better on the wire, same in the app.** Sources keep per-site order (0 gaps measured), but a consumer redelivery after `ack_wait` can reorder. Keep `FleetRegistry`'s sequence guard | Measured order; redelivery is by design |
| 12 | **Retained health + Last Will** | **No LWT.** Last health = `last_per_subject` on `FLEET_UPLINK`; gateway-down = leaf DISCONNECT event (row 6); per-robot = heartbeat staleness, as today | Design + row 6 |
| 13 | **Sessions for decommissioned sites accumulate** | **Same class.** Explicitly delete the site's consumer, its `FLEET_UPLINK` source and its hub account (regenerate `hub-accounts.conf` without it) | Design |

## Behaviours found while testing the NATS design

| # | Behaviour | Impact | Evidence |
|---|---|---|---|
| 14 | **Source retry backoff.** A source that keeps failing during an outage retries after `5s × fails×2`, capped at **2 m** | After a long outage the site is back in seconds, but catch-up can start up to 2 m later. Nothing is lost; commands are unaffected (pulled). Size site storage and alert thresholds for it | Source: `server/stream.go` `retryBackOff`, `retryMaximum`. Measured: reconnect +2 s, copy done +58 s (3-minute outage); +37–48 s in other runs |
| 15 | **Ephemeral R1 consumers after a hub node loss.** For ~3 minutes about 1 in 3 creates time out (placed on the dead peer). nats.go `OrderedConsumer` (an R1 ephemeral) then blocked past its own max-wait | Cloud apps must use durable R3 consumers, or create ephemerals with a short timeout and retry. Durable R3 consumers (site command pulls, uplink copy) were unaffected | Measured: probe every 1 s after `kill -9`: 4/6, 3/6, 2/9, 4/5, 1/10, 2/8, 2/8, 10/20, 7/22, 7/24 failed per window, then 0/30, 0/30 from ~3 min. Not root-caused |
| 16 | **Domain prefix intercepted.** The hub answers `$JS.cloud.API.*` itself in every account, so a site account can't import FLEET's API under that prefix | Sites use `$JS.hub.API` for their command consumer; `gen-hub-accounts.sh` maps it | Measured: "JetStream not enabled for account" until the prefix was changed |
| 17 | **Route admission.** Sites and hubs share the fleet CA, so plain `verify: true` on routes would admit a site cert | Routes use `verify_cert_and_check_known_urls` (peer needs a DNS SAN matching a route host; site certs have none) | Source: `server/auth.go`. Measured: site cert as a route, rejected 48/48 attempts, never a replica |

## Production verdict

**The design is sound for production. This repo is not a deployment yet.** Every
failure that shaped the MQTT design either disappears (1, 2, 3, 6, 8) or has a
known, configured mitigation (7, 9). The two that remain are the same class as
before: silent failures (4, 5) that only monitoring catches, and at-least-once
semantics (10, 11) that the apps already handle. Rows 14–15 are NATS behaviours
the apps and alerting must allow for.

### Done in this repo
- **3-node hub cluster, R3 streams.** Stream leader `kill -9` at 1,334 of 100,000
  copied: new leader finished 100,000/100,000 in 11 s, 0 gaps; queued command
  delivered; the node rejoined with lag 0 (`test/scenario-cluster.sh`).
- **Per-site accounts.** Each site's leaf binds to its own hub account, with
  imports/exports only for its own stream, consumer and flow control. A site1
  user with full rights, subscribed to `>` while site2 worked, saw 0 site2
  messages (`test/scenario.sh`). The same sniff against the old single-account
  config also saw none, so the old gap was a publish-side risk (a site guessing
  another site's random reply token), not an observed leak. It is now
  structurally closed.

### Must do before production
1. **Load test at fleet size.** Untested: one `FLEET_UPLINK` with 500 sources, and
   500 sites reconnecting after a regional outage and draining backlogs at once. If
   either is a problem, shard `FLEET_UPLINK` by region.
2. **Pin and retest the server version.** Tested 2.12.1; current upstream is newer.
3. **Monitoring** (the only defence against rows 4 and 5):
   - per-source `Active` (last seen) on `FLEET_UPLINK`
   - leaf DISCONNECT events
   - `FLEET_COMMANDS` per-consumer pending and oldest age
   - site stream fill against `max_bytes` (`discard: new` refuses publishes when full)
   - allow for row 14: "last seen" can lag up to ~2 m after a reconnect
4. **Power-cut test on real site hardware** with `sync_interval: always`, and measure
   its throughput cost there. This VM's disk gave identical timings with and without
   it (0.51 s for 2000 publishes), which suggests fsync isn't honoured here, so that
   result says nothing about real hardware.
5. **Certificate lifecycle**: rotation runbook, plus revocation (OCSP or a CRL)
   on the leaf and WebSocket listeners.
