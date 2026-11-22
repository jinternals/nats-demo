# 2 Mbps WAN Load Test Results (Pattern A)

Measured on **2026-11-23** against [Pattern A (Cloud NATS Hub + Leaf + `nats-io/nats-kafka`)](../pattern-a-cloud-hub/README.md) with `site1`'s WAN interface throttled to **2 Mbps**.

## Test setup

- **Stack:** `nats:2.12.5` (3-node cloud hub + `site1` leaf with `sync_interval: always`), `apache/kafka:3.9.1` (KRaft), `natsio/nats-kafka:1.4.2`.
- **Bandwidth limit:** Linux `tc` token-bucket filter applied only to `site1`'s WAN interface (`eth0` on `nats-edge-wan`; site LAN left unthrottled):
  ```bash
  tc qdisc replace dev eth0 root tbf rate 2mbit burst 32kbit latency 50ms
  ```
- **Workload:** `5,000` messages × `512 B` payload (`2.56 MB` total), published synchronously to `site1-outbox` (`device.to.site.load-01.telemetry`) by 4 concurrent clients (`nats bench js pub sync`).
- **Concurrent downlink test:** while `site1`'s 2 Mbps uplink was saturated draining the backlog, a command was produced to Kafka `cloud.to.site.site1.command` and pulled by `site1-controller` from `cloud-outbox` over the same leaf connection.

## Results

| Metric | Measured value | Notes |
|---|---|---|
| **Messages published** | `5,000` × `512 B` (`2.56 MB` payload) | 4 concurrent sync JetStream publishers |
| **Local site ingest (`site1-outbox`)** | **544 msg/s** (`272 KiB/s`) in `9.69 s` | `sync_interval: always` (fsync every write) |
| **Local publish latency** | P50 `5.85 ms` · P90 `14.05 ms` · P99 `16.88 ms` · max `29.25 ms` | Acked by `site1` disk, independent of WAN |
| **Leaf wire bytes (`/leafz`)** | `2,560,465` bytes (`2.44 MiB`) | NATS leaf `out_bytes` delta |
| **Uplink → Cloud NATS (`cloud-inbox`)** | **`12.50 s`** — **`400 msg/s`**, **`1.64 Mbps`** effective | Remaining ~0.36 Mbps is TLS + TCP/IP + NATS framing |
| **Uplink → Cloud Kafka (`site.to.cloud.telemetry`)** | **`12.57 s`** — **`398 msg/s`** | `nats-kafka` added ~`70 ms` behind `cloud-inbox` |
| **Downlink command during 2 Mbps saturation** | **`1.42 s`** end-to-end | Kafka → `nats-kafka` → `cloud-outbox` → `site1-controller` (includes CLI container startup) |
| **Message loss** | **`0 / 5,000`** | `site1-outbox` = `cloud-inbox` = Kafka = `5,000` |

## Takeaways

1. **Sustainable uplink ceiling at 2 Mbps:** ~**380–400 msg/s** at 512 B/msg (~1.64 Mbps payload throughput over TLS).
2. **Burst absorption:** local devices publish to `site1-outbox` at local disk speed (~544 msg/s with 4 sync clients); any burst above 2 Mbps buffers on `site1`'s disk (`max_bytes: 15 GB`) and drains cleanly with zero loss.
3. **Commands are not starved:** downlink commands still arrived from Kafka to `site1-controller` in ~1.4 s while the 2 Mbps uplink was saturated.
4. **No bridge bottleneck:** `nats-io/nats-kafka` kept up with `cloud-inbox` within ~70 ms.
