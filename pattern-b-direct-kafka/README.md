# Pattern B: Direct Site-to-Kafka (no Cloud NATS hub)

Each site runs its own local **NATS** server (`<site>-outbox`, `<site>-inbox`)
and its own local **[`nats-io/nats-kafka`](https://github.com/nats-io/nats-kafka)**
bridge that connects **directly to Cloud Kafka** over the WAN. There is no
Cloud NATS hub cluster.

- **Down (commands):** Cloud service → Cloud Kafka `cloud.to.site.<site>.command`
  → site `<site>-bridge` (`KafkaToJetStream`) → site NATS `<site>-inbox` →
  site controller → device.
- **Up (events):** Device → site NATS `<site>-outbox` → site `<site>-bridge`
  (`JetStreamToKafka`) → Cloud Kafka `site.to.cloud.<type>` (key = `<site>`) →
  cloud services.

See [docs/comparison.md](../docs/comparison.md) and
[ADR-0003](../docs/adr/0003-direct-site-to-kafka-vs-cloud-hub.md) for how this
compares with [Pattern A (Cloud NATS Hub)](../pattern-a-cloud-hub/README.md).

## Architecture

```mermaid
flowchart LR
  subgraph site1["SITE: site1 (edge LAN)"]
    d1["Devices"] <--> s1[("site1 NATS<br/>site1-outbox<br/>site1-inbox")]
    k1["Site controller"] <--> s1
    s1 <--> b1["site1-bridge<br/>(nats-kafka)"]
  end

  subgraph site2["SITE: site2 (edge LAN)"]
    d2["Devices"] <--> s2[("site2 NATS<br/>site2-outbox<br/>site2-inbox")]
    k2["Site controller"] <--> s2
    s2 <--> b2["site2-bridge<br/>(nats-kafka)"]
  end

  subgraph cloud["CLOUD (VPC, no NATS hub)"]
    kafka[("Cloud Kafka<br/>cloud.to.site.&lt;site&gt;.command<br/>site.to.cloud.&lt;type&gt;")]
    svc["Cloud services"]
    kafka <--> svc
  end

  b1 <== "WAN: Kafka protocol<br/>(TLS + SASL/mTLS :9093)" ==> kafka
  b2 <== "WAN: Kafka protocol" ==> kafka
```

## Up: device → Cloud Kafka

```mermaid
sequenceDiagram
  participant D as Device arm-01
  participant S as site1 NATS (site1-outbox)
  participant B as site1-bridge (nats-kafka)
  participant K as Cloud Kafka

  D->>S: device.to.site.arm-01.telemetry
  Note over S: subject transform stores as site.to.cloud.site1.telemetry
  S-->>D: ack (fsynced to site1 disk)
  Note over B,K: If WAN is down, events buffer in site1-outbox
  S->>B: JetStreamToKafka (durable kafka-bridge-site1-telemetry)
  B->>K: site.to.cloud.telemetry (key: site1)
  K-->>B: ack
  B->>S: ack
```

## Down: Cloud Kafka → device

```mermaid
sequenceDiagram
  participant C as Cloud service
  participant K as Cloud Kafka
  participant B as site1-bridge (nats-kafka)
  participant S as site1 NATS (site1-inbox)
  participant CTL as Site controller
  participant D as Device

  C->>K: cloud.to.site.site1.command (1 partition)
  Note over K,B: If WAN is down, commands wait in Kafka
  K->>B: KafkaToJetStream (group kafka-bridge-site1)
  B->>S: publish cloud.to.site.site1.command
  S-->>B: ack (stored in site1-inbox)
  B->>K: commit offset
  CTL->>S: pull local consumer site1-controller
  S-->>CTL: command
  CTL->>D: request site.to.device.arm-01.command
  D-->>CTL: reply: ok
  CTL->>S: ack (deleted from workqueue)
```

## Key differences from Pattern A

- **No Cloud NATS cluster to run.** Cloud infrastructure is just Kafka.
- **Bridge runs at every site.** Every site deploys and upgrades `nats-kafka`
  alongside its site NATS server.
- **Kafka is exposed to sites over WAN.** Production requires Kafka TLS +
  SASL/mTLS (and per-site Kafka ACLs restricting `site1` to
  `cloud.to.site.site1.command` and `site.to.cloud.*`). Kafka's binary protocol
  cannot traverse standard HTTPS-only corporate proxies (unlike Pattern A's
  `wss://` leaf nodes).
- **Commands live in Kafka during WAN outages.** Commands are not expired by
  `Nats-TTL` until the site reconnects and pulls them, so the site controller
  must check an `expiresAt` timestamp in the payload.

## Run it

```bash
cd pattern-b-direct-kafka
make up              # generates config, starts Kafka, site1+bridge, site2+bridge
make kafka-topics    # list Kafka topics and partitions
make status          # site1 and site2 JetStream streams
make kafka-status    # per-site Kafka consumer group lag
make down            # stop and delete all data
```

Try a WAN outage by hand:

```bash
make cut-site1       # disconnects site1-bridge from WAN; devices keep publishing to site1
make restore-site1   # site1-bridge reconnects and drains site1-outbox to Cloud Kafka
```

### Try it from Kafka

Send a command to `site1`:

```bash
echo 'site1|{"commandId":"c-42","action":"start"}' | \
  docker compose exec -T kafka /opt/kafka/bin/kafka-console-producer.sh \
  --bootstrap-server localhost:9092 \
  --topic cloud.to.site.site1.command \
  --property parse.key=true --property key.separator='|'
```

Pull it as `site1-controller` (local to `site1`):

```bash
docker compose run --rm --entrypoint sh setup -c \
  'nats --server nats://site1:4222 --user site-controller --password "$SITE_CONTROLLER_PASS" consumer next site1-inbox site1-controller --count 1 --ack'
```

Read telemetry in Cloud Kafka:

```bash
docker compose exec kafka /opt/kafka/bin/kafka-console-consumer.sh \
  --bootstrap-server localhost:9092 \
  --topic site.to.cloud.telemetry \
  --from-beginning --property print.key=true
```

## Endpoints

| Service | Address | Notes |
|---|---|---|
| Cloud Kafka | `localhost:9094` | plaintext (dev only) |
| `site1` NATS | `nats://localhost:4223` (monitor `:8223`) | users: `device`, `site-controller`, `site-ops` |
| `site2` NATS | `nats://localhost:4224` (monitor `:8224`) | users: `device`, `site-controller`, `site-ops` |
| `site1-bridge` monitor | `http://localhost:8225/varz` | `/healthz`, `/varz` |
| `site2-bridge` monitor | `http://localhost:8226/varz` | `/healthz`, `/varz` |
