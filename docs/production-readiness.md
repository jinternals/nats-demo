# Production readiness

Can this design replace the MQTT bridge design in
[`jinternals/mqtt`](https://github.com/jinternals/mqtt)? Below, each problem
that design had to work around is checked against this one.

Evidence:
- **e2e**: a test in `pattern-a-cloud-hub/e2e/`, run against `make up` (nats-server 2.12.5).
- **4338c26**: measured with the earlier test scripts in commit `4338c26`
  (`test/scenario*.sh`, nats-server 2.12.1). Not yet ported to `e2e/`.
- **source**: read in the nats-server source.

## The MQTT design's problems

| MQTT problem | Here | Evidence |
|---|---|---|
| Bridge session lost on reconnect, so queued commands were dropped (MQTT 5 session expiry) | **Gone.** Commands wait in `cloud-outbox`; the durable consumer isn't tied to a connection | e2e `TestStaleCommandsExpireInCloud`, `TestHubLeaderLossDuringCatchUp` |
| Outbound spool lost data (NanoMQ) | **Gone.** The site stream is the spool; the app's ack comes from site disk | e2e `TestUplinkSurvivesWANOutage`, `TestSiteServerCrashDuringOutage` |
| Message expiry lost on the 3.1.1 hop | **Gone.** `Nats-TTL` deletes stale commands in the cloud | e2e `TestStaleCommandsExpireInCloud` |
| WAN health via a bridge state topic | **Built in.** `$SYS.ACCOUNT.<site>.DISCONNECT` / `CONNECT` with the site's cert identity | e2e `TestSiteOfflineEvent` (disconnect seen after 61–74 s) |
| Reconnect stampede | **Built in.** Reconnect delay is randomised (`reconnect: 5s` gives 5–10 s) | source `server/leafnode.go` |
| Silent link (socket open, nothing flows) | **Configured.** `ping_interval 20s`, `ping_max 2`; the default (2m × 2) takes minutes | e2e `TestSiteOfflineEvent`; 4338c26 (61 s) |
| Crash and power durability (mosquitto autosave every 30 s) | **Crash: nothing lost. Power cut: configured.** `sync_interval: always` on sites; the default fsync interval is 2 m | e2e `TestSiteServerCrashDuringOutage`; source `defaultSyncInterval`. Power cut **not tested** |
| Denied actions reported as success (PUBACK 0) | **Partly better.** On a site server a denied publish is an explicit error. A request outside a site's hub account gets `no responders` and the hub logs nothing | e2e `TestSitesAreIsolated` |
| Silent one-way failure: telemetry flows, commands vanish | **Still possible.** A missing import/export stops a copy without an error. Generate accounts, alert on source *last seen* | 4338c26 |
| Duplicates | **Same: at-least-once.** Dedupe commands on `commandId`; set `Nats-Msg-Id` on publishes | design |
| Reordering after a backlog | **Ordered on the wire** (per site); a redelivery after `ack_wait` can still reorder in the app | e2e `assertInOrder` |
| Retained health + Last Will | **No equivalent.** Latest health: consumer with `deliver_policy: last_per_subject`; offline: the DISCONNECT event | design |

## Behaviours of this design to plan for

| Behaviour | Impact | Evidence |
|---|---|---|
| Catch-up can start up to 2 m after a site reconnects (source retry backoff) | Size alerting for it; no data loss; commands unaffected | source `retryMaximum`; 4338c26: 37–58 s |
| After a hub node dies, short-lived (R1) consumers can fail to create for a few minutes | Cloud apps: use durable consumers, or retry with a short timeout | 4338c26: ~1 in 3 creates failed for ~3 min |
| A cut can make the site resend events already written to the dead connection | Extra WAN traffic after an outage; the cloud stores each event once | e2e `TestUplinkCopiesEventByEvent` |
| The uplink copy can't be rate-limited | It uses the link as fast as flow control allows | source `StreamSource`; see guide, *Delivery speed* |
| Sites can't import the hub's API under `$JS.cloud.API` | Sites use `_edge.api` | 4338c26 |
| A site cert could join the hub cluster if routes used plain `verify` | Routes use `verify_cert_and_check_known_urls` | 4338c26: rejected 48 of 48 attempts |

## Done

- 3-node hub cluster, every cloud stream replicated 3×. Losing the stream
  leader mid catch-up loses nothing (e2e `TestHubLeaderLossDuringCatchUp`).
- One hub account per site (e2e `TestSitesAreIsolated`).
- One naming convention ([naming.md](naming.md)), checked on every config
  file (`TestNamingConvention`) and enforced per role
  (`TestRolesUseOnlyTheirHop`). The cloud never sees device ids
  (`TestForwardUpDeviceToCloud`).
- Leaf over `wss://` through an HTTP proxy for HTTPS-only sites (4338c26).
- Cloud services on Kafka, joined by the official `nats-io/nats-kafka` bridge
  ([ADR-0002](adr/0002-kafka-nats-bridge.md)) with zero custom bridge code.
  Command to device and result back, per-type up topics keyed by site, outage
  catch-up in order, bridge `kill -9`, least-privilege NATS user, and topic
  naming checks (e2e `TestKafka*`).

## Kafka bridge (`nats-io/nats-kafka`): behaviours to plan for

| Behaviour | Impact | Evidence |
|---|---|---|
| One Kafka command topic per site (`cloud.to.site.<site>.command`) | Adding a site adds a topic and a `KafkaToJetStream` connector (`scripts/generate.sh`); fine for 50–500 sites, revisit above ~1,000 | [ADR-0002](adr/0002-kafka-nats-bridge.md) |
| At-least-once in both directions | Deduplicate commands on `Nats-Msg-Id` / `commandId` and events on `Nats-Msg-Id` / sequence | design |
| `Nats-TTL` passes through unchanged (starts ticking at NATS ingest, not Kafka produce) | Include `expiresAt` in the command payload if commands might sit in Kafka while the bridge is down | design |
| Kafka down: events back up in `cloud-inbox` | Size `cloud-inbox` `max_age` (7 d) / disk for the longest Kafka outage | design |
| Bridge down: commands wait in Kafka, events wait in `cloud-inbox` | Both catch up after restart; alert on consumer group lag and `cloud-inbox` pending acks (`make kafka-status`, `/varz`) | e2e `TestKafkaBridgeCrashLosesNothing` |

## Still needed before production

1. **Load test at fleet size:** hundreds of sources on `cloud-inbox`, and
   many sites reconnecting and catching up at once. Tested with 2 sites.
2. **Power-cut test** on real site hardware, and the throughput cost of
   `sync_interval: always` there.
3. **Monitoring:** DISCONNECT events, per-source *last seen*, command queue
   age, site stream fill, `nats-kafka` `/healthz` & `/varz`, and bridge lag on
   both Kafka and `cloud-inbox`.
4. **Certificate lifecycle:** rotation, and revocation (OCSP or a CRL) on the
   leaf and WebSocket listeners.
5. **Port the remaining 4338c26 checks to `e2e/`:** WebSocket through a
   proxy, rogue route, consumer creation after a node loss.
6. **Kafka for real:** 3+ brokers, replication 3, `min.insync.replicas` 2,
   SASL/mTLS (`tls` and `sasl` blocks in `nats-kafka.conf`) and ACLs (guide,
   *Security*), plus a bridge throughput test at fleet event rate.
