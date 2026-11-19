# Naming

One rule set for everything that has a name. `TestNamingConvention`
(`e2e/naming_test.go`) checks every subject, stream and consumer name in
`config/` against it.

## The model

The cloud talks to **sites**. A site talks to its **devices**. The cloud never
needs to know a device, so device ids never cross the WAN.

```
cloud  ──cloud.to.site──▶  site  ──site.to.device──▶  device
cloud  ◀──site.to.cloud──  site  ◀──device.to.site──  device
```

## Subjects: `<from>.to.<to>.<id>.<type>`

| Hop | Subject | Example |
|---|---|---|
| Cloud → site | `cloud.to.site.<site>.<type>` | `cloud.to.site.site1.command` |
| Site → cloud | `site.to.cloud.<site>.<type>` | `site.to.cloud.site1.telemetry` |
| Site → device | `site.to.device.<device>.<type>` | `site.to.device.arm-01.command` |
| Device → site | `device.to.site.<device>.<type>` | `device.to.site.arm-01.telemetry` |

`<id>` is the id of the side that isn't fixed by the hop: the site on the WAN
hops, the device on the local hops. Every subject has exactly five tokens.

### Types

| Type | Goes | Meaning |
|---|---|---|
| `telemetry` | up | measurements a device reports |
| `health` | up | device status (alive, faults) |
| `result` | up | outcome of a command |
| `command` | down | something to do |

A new type is added here, and to `hops` in `e2e/naming_test.go`, before
anyone uses it. Up-types are forwarded to the cloud only if they are listed in
the site's outbox stream (`scripts/generate.sh`).

### Ids

- Site and device ids: lowercase `a-z`, `0-9` and `-`, starting with a letter
  or digit (`site1`, `arm-01`). Never a dot, because a dot starts a new token.
- The same site id is used everywhere: subjects, hub account, JetStream
  domain, server name, certificate CN and stream names.

### What never goes in a subject

- Message, command or trace ids: use headers (`Nats-Msg-Id`, `Command-Id`).
- Schema versions: use a header (`Schema`) or the payload.
- Environment (`prod`, `dev`): use separate clusters and accounts.
- Device ids on the WAN hops: put them in the payload if the cloud ever needs
  them.

### Wildcards

| Want | Use |
|---|---|
| everything any site sends to the cloud | `site.to.cloud.>` |
| one site's telemetry | `site.to.cloud.site1.telemetry` |
| everything the cloud sends to site1 | `cloud.to.site.site1.>` |
| everything one device sends | `device.to.site.arm-01.>` |

Rules and permissions always start with the full hop (`site.to.cloud.>`),
never a shorter prefix such as `site.>`, which would match two hops.

### Reserved prefixes

| Prefix | Owner | Used for |
|---|---|---|
| `_edge.api.*` | this project | a site controller reaching its consumer in the cloud |
| `_edge.copy.<site>.*` | this project | the copy of `<site>-outbox` into `cloud-inbox` |
| `$JS.*`, `$SYS.*`, `$KV.*`, `$O.*`, `_INBOX.*` | NATS | JetStream API, acks, flow control, system events, replies |

Applications publish only on the four hops. Don't create subjects under any
reserved prefix.

## Forwarding rules

| Direction | Rule | Where |
|---|---|---|
| Up | `device.to.site.<device>.<type>` → `site.to.cloud.<site>.<type>` for `telemetry`, `health`, `result` | subject transform on `<site>-outbox` |
| Up | `<site>-outbox` → `cloud-inbox` | source on `cloud-inbox` |
| Down | `cloud.to.site.<site>.command` → `<site>-controller` consumer | filter on the consumer |
| Down | command → `site.to.device.<device>.command` | the site controller (application) chooses the device |

Down needs an application because the cloud doesn't name the device: a rule
can only rewrite subjects, it can't decide which device acts.

## Headers

`Title-Case`, documented here before use. Never invent a header starting with
`Nats-`; NATS reserves that prefix.

| Header | Set by | Meaning |
|---|---|---|
| `Nats-Msg-Id` | publisher | event or command id; JetStream drops duplicates with the same id |
| `Nats-TTL` | cloud | how long a command may wait before it is deleted |
| `Command-Id` | site controller, device | the command a result belongs to (optional; also in the payload) |
| `Schema` | publisher | payload schema name and version, e.g. `telemetry/v1` (optional) |

## JetStream

| Kind | Pattern | Names |
|---|---|---|
| Stream | `<place>-inbox` / `<place>-outbox`, named from where it lives | `site1-outbox`, `cloud-inbox`, `cloud-outbox` |
| Consumer | `<reader>-<role>` | `site1-controller` |

| Stream | Lives in | Holds |
|---|---|---|
| `<site>-outbox` | site | device events waiting to go to the cloud |
| `cloud-inbox` | cloud | events from all sites |
| `cloud-outbox` | cloud | commands for all sites |

Stream and consumer names can't contain dots, so they use `-`.

## Servers, accounts, users, certificates

| Thing | Names |
|---|---|
| Hub servers | `cloud-hub-1`, `cloud-hub-2`, `cloud-hub-3`; cluster `cloud`; DNS name for sites `cloud-hub` |
| Site servers | the site id: `site1` |
| JetStream domains | `cloud`; the site id on each site |
| Hub accounts | `cloud`, one per site (`site1`, …), `system` |
| Site accounts | `site` (everything on the site LAN), `system` |
| Users, by role | `cloud-app`, `site-controller`, `device`, `site-ops`, `sys` |
| Site certificate | `CN=<site>,O=fleet`, which is also its hub user |
| Hub certificate | DNS names `cloud-hub`, `cloud-hub-1..3` |

## Who may use what

| User | Publish | Subscribe |
|---|---|---|
| `device` | `device.to.site.>`, plus replies to commands it received | `site.to.device.>` |
| `site-controller` | `site.to.device.>`, its own consumer on `cloud-outbox` | `device.to.site.>` |
| `cloud-app` | `cloud.to.site.>`, JetStream API | replies |
| site's hub account | only `site.to.cloud.<site>.*` (via its outbox) and pulls of `<site>-controller` | |

`TestRolesUseOnlyTheirHop` checks that each user is refused the other hops.

## Don't

- Abbreviate: `command`, not `cmd`.
- Use `site.>` or `cloud.>` in a rule or permission.
- Put ids, versions or environments in subjects.
- Hand-edit `config/generated/`: change `scripts/generate.sh` instead.
