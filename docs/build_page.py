# Builds docs/index.html from the real config files. Run: python3 docs/build_page.py
import html, pathlib
R = pathlib.Path(__file__).resolve().parent.parent
def code(p):
    return '<pre><code>' + html.escape((R/p).read_text()) + '</code></pre>'

svg = '''<svg viewBox="0 0 760 520" role="img" aria-labelledby="dt dd" class="diagram">
<title id="dt">3-node cloud hub with per-site accounts and site leaf nodes</title>
<desc id="dd">Each site runs a leaf node with a local uplink stream; the leaf dials out to the cloud hub over mutual TLS using a per-site certificate. Commands wait in the cloud; uplink data waits on the site disk.</desc>
<defs><marker id="ah" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M2 1L8 5L2 9" fill="none" stroke="context-stroke" stroke-width="1.6"/></marker></defs>
<rect x="20" y="16" width="720" height="176" rx="18" class="cloud"/>
<text x="380" y="44" class="h">Cloud hub · 3-node cluster · domain <tspan class="m">cloud</tspan> · one account per site</text>
<rect x="50" y="64" width="300" height="70" rx="10" class="store"/>
<text x="200" y="92" class="b m">FLEET_UPLINK</text><text x="200" y="114" class="s">sources every site stream · R3</text>
<rect x="410" y="64" width="300" height="70" rx="10" class="store"/>
<text x="560" y="92" class="b m">FLEET_COMMANDS</text><text x="560" y="114" class="s">work queue · Nats-TTL per command · R3</text>
<text x="380" y="168" class="s">account FLEET · cloud-app · accounts SITE1, SITE2 import only their own consumer</text>
<rect x="20" y="300" width="340" height="204" rx="18" class="site"/>
<text x="44" y="326" class="h" style="text-anchor:start">Site 1 LAN</text>
<rect x="45" y="342" width="290" height="66" rx="10" class="store"/>
<text x="190" y="368" class="b m">site1-leaf</text><text x="190" y="390" class="s">domain site1 · SITE1_UPLINK on disk</text>
<rect x="45" y="440" width="290" height="44" rx="10" class="app"/><text x="190" y="467" class="b">edge-app</text>
<rect x="400" y="300" width="340" height="204" rx="18" class="site"/>
<text x="424" y="326" class="h" style="text-anchor:start">Site 2 LAN</text>
<rect x="425" y="342" width="290" height="66" rx="10" class="store"/>
<text x="570" y="368" class="b m">site2-leaf</text><text x="570" y="390" class="s">domain site2 · SITE2_UPLINK on disk</text>
<rect x="425" y="440" width="290" height="44" rx="10" class="app"/><text x="570" y="467" class="b">edge-app</text>
<line x1="190" y1="438" x2="190" y2="412" class="ln" marker-end="url(#ah)" marker-start="url(#ah)"/>
<line x1="570" y1="438" x2="570" y2="412" class="ln" marker-end="url(#ah)" marker-start="url(#ah)"/>
<line x1="190" y1="340" x2="190" y2="196" class="wan" marker-end="url(#ah)"/>
<line x1="570" y1="340" x2="570" y2="196" class="wan" marker-end="url(#ah)"/>
<text x="204" y="250" class="l">leaf · mTLS · dials out</text><text x="204" y="270" class="l m">CN=leaf-site1,O=fleet</text>
<text x="584" y="250" class="l">leaf · mTLS · dials out</text><text x="584" y="270" class="l m">CN=leaf-site2,O=fleet</text>
</svg>'''

page = f'''<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>NATS edge: cloud hub + site leaf nodes</title>
<style>
:root{{--bg:#fbfaf7;--fg:#1f1e1b;--mut:#5f5e5a;--line:#d3d1c7;--card:#fff;--acc:#534ab7;--acc-bg:#eeedfe;--st:#e1f5ee;--st-b:#0f6e56;--site:#f1efe8;--code:#f4f3ee;--ok:#3b6d11;--bad:#a32d2d}}
@media (prefers-color-scheme:dark){{:root{{--bg:#1a1a18;--fg:#ecebe6;--mut:#b4b2a9;--line:#444441;--card:#232321;--acc:#afa9ec;--acc-bg:#26215c;--st:#04342c;--st-b:#5dcaa5;--site:#2c2c2a;--code:#262624;--ok:#97c459;--bad:#f09595}}}}
*{{box-sizing:border-box}}body{{margin:0;background:var(--bg);color:var(--fg);font:16px/1.65 -apple-system,BlinkMacSystemFont,"Segoe UI",Inter,sans-serif}}
main{{max-width:960px;margin:0 auto;padding:32px 20px 80px}}
h1{{font-size:30px;margin:0 0 6px;font-weight:600;letter-spacing:-.01em}}h2{{font-size:21px;margin:44px 0 10px;padding-top:12px;border-top:1px solid var(--line);font-weight:600}}h3{{font-size:17px;margin:24px 0 6px}}
.lede{{color:var(--mut);font-size:18px;margin:0 0 22px}}
.tags span{{display:inline-block;font-size:12px;padding:3px 10px;border-radius:99px;background:var(--acc-bg);color:var(--acc);margin:0 6px 6px 0}}
code,.m{{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:.88em}}
pre{{background:var(--code);border:1px solid var(--line);border-radius:10px;padding:14px 16px;overflow:auto;font-size:13px;line-height:1.5}}
pre code{{font-size:inherit}}
table{{width:100%;border-collapse:collapse;margin:10px 0 6px;font-size:14.5px}}th,td{{text-align:left;vertical-align:top;padding:9px 10px;border-bottom:1px solid var(--line)}}th{{color:var(--mut);font-weight:600;font-size:13px}}
.card{{background:var(--card);border:1px solid var(--line);border-radius:14px;padding:14px}}
.diagram{{width:100%;height:auto;display:block}}
.diagram .cloud{{fill:var(--acc-bg);stroke:var(--acc);stroke-width:1}}.diagram .site{{fill:var(--site);stroke:var(--line);stroke-width:1}}
.diagram .store{{fill:var(--st);stroke:var(--st-b);stroke-width:1}}.diagram .app{{fill:var(--card);stroke:var(--line);stroke-width:1}}
.diagram text{{text-anchor:middle;fill:var(--fg);font-size:14px}}.diagram .h{{font-weight:600;font-size:15px}}.diagram .b{{font-weight:600}}.diagram .s{{fill:var(--mut);font-size:12.5px}}
.diagram .l{{text-anchor:start;fill:var(--mut);font-size:12.5px}}.diagram .ln{{stroke:var(--mut);stroke-width:1.4}}.diagram .wan{{stroke:var(--acc);stroke-width:2.6}}
.ok{{color:var(--ok);font-weight:600}}.bad{{color:var(--bad);font-weight:600}}
details{{margin:10px 0;border:1px solid var(--line);border-radius:10px;background:var(--card)}}summary{{cursor:pointer;padding:10px 14px;font-weight:600}}details pre{{margin:0 12px 12px}}
.note{{border-left:3px solid var(--acc);padding:4px 14px;margin:14px 0;color:var(--mut)}}
nav{{display:flex;flex-wrap:wrap;gap:4px 14px}}nav a{{color:var(--acc);font-size:14px;text-decoration:none}}
.dwrap{{overflow-x:auto}}@media (max-width:640px){{.diagram{{min-width:620px}}table{{display:block;overflow-x:auto}}h1{{font-size:25px}}}}
td code{{word-break:break-word}}
</style></head><body><main>
<h1>NATS edge: cloud hub + site leaf nodes</h1>
<p class="lede">Sites subscribe to the cloud over one outbound TLS connection. Reliable delivery in both directions, a 3-node hub cluster, and a separate certificate and hub account for every site.</p>
<div class="tags"><span>nats-server 2.12.1</span><span>JetStream domains</span><span>leaf nodes</span><span>mTLS verify_and_map</span><span>account per site</span><span>3-node R3</span><span>wss:// fallback</span><span>Nats-TTL</span><span>verified locally</span></div>
<nav><a href="#arch">Architecture</a><a href="#flows">Delivery</a><a href="#sec">Security</a><a href="#config">Config</a><a href="#verify">Verification</a><a href="#behave">Behaviours</a><a href="#gaps">Still needed</a></nav>

<h2 id="arch">Architecture</h2>
<div class="card dwrap">{svg}</div>
<p>Edge apps talk only to the NATS server on their own site LAN. The leaf node at each site <b>dials out</b> to the hub, so the site needs no inbound port and works behind NAT. Each side runs its own JetStream domain (<code>cloud</code>, <code>site1</code>, …), so the cloud can reach a site's streams as <code>$JS.site1.API</code>, and a site reaches its own command consumer in the cloud through <code>$JS.hub.API</code>, both over the same leaf connection. The hub is a 3-node cluster with R3 streams. Sites whose egress allows only HTTPS use the same leaf over <code>wss://</code> on 443, through an HTTP CONNECT proxy if needed.</p>

<h2 id="flows">Where each message waits during an outage</h2>
<table><tr><th>Traffic</th><th>Waits on</th><th>Mechanism</th></tr>
<tr><td>Telemetry, health, command results (site → cloud)</td><td><b>Site disk</b>: <code>SITE1_UPLINK</code></td><td>The edge app's JetStream publish is acked by the <i>local</i> server, so it succeeds with the WAN down. <code>FLEET_UPLINK</code> <i>sources</i> each site stream and resumes from its last sequence when the link returns. Order is kept, and <code>Nats-Msg-Id</code> deduplicates.</td></tr>
<tr><td>Commands (cloud → site)</td><td><b>Cloud disk</b>: <code>FLEET_COMMANDS</code> (R3)</td><td>The site pulls its own durable consumer <code>siteN-commands</code> through the leaf. Explicit ack; a work-queue stream deletes on ack.</td></tr>
<tr><td>Stale commands</td><td>Deleted in the cloud</td><td>Per-message <code>Nats-TTL</code> (stream has <code>allow_msg_ttl</code>), so a stale command is never shipped. Keep the <code>expiresAt</code> check at the edge as well.</td></tr>
<tr><td>Latest health per robot</td><td><code>FLEET_UPLINK</code></td><td>A consumer with <code>deliver_policy: last_per_subject</code> on <code>sites.*.health.&gt;</code></td></tr></table>
<p class="note">Sizing: <code>SITE1_UPLINK</code> uses <code>discard: new</code>. When the site disk fills during a very long outage, publishes are <i>refused</i> (the app sees an error) rather than old unsent data being silently dropped. Size <code>max_bytes</code> from uplink rate × the longest outage you must survive.</p>

<h2 id="sec">Per-leaf security</h2>
<h3>Identity: one client certificate per site</h3>
<p>The hub's leaf listener uses <code>verify_and_map: true</code>. Each site presents its own certificate signed by the fleet CA, and the certificate subject <code>CN=leaf-siteN,O=fleet</code> <i>is</i> the hub user. There is no shared secret and no password in a URL. Revoking one site touches no other site. A valid CA-signed certificate with an unknown subject is rejected.</p>
<h3>Isolation: one hub account per site</h3>
<p>Each site's leaf binds to its <b>own</b> hub account (<code>SITE1</code>, <code>SITE2</code>, …); the cloud lives in <code>FLEET</code>. An account sees only what it imports, so isolation is the account boundary, not a list of subject permissions. <code>gen-hub-accounts.sh</code> generates exactly this per site:</p>
<table><tr><th>Direction</th><th>What crosses</th><th>Why</th></tr>
<tr><td>SITE1 → FLEET</td><td>service <code>$JS.site1.API.&gt;</code><br>service <code>$JS.FC.SITE1_UPLINK.&gt;</code><br>stream <code>fleet.deliver.site1.&gt;</code></td><td>the hub sources <code>SITE1_UPLINK</code>, with its flow control and data</td></tr>
<tr><td>FLEET → SITE1</td><td>service <code>CONSUMER.MSG.NEXT</code> / <code>INFO</code> on <code>FLEET_COMMANDS.site1-commands</code> (as <code>$JS.hub.API.*</code>)<br>service <code>$JS.ACK.FLEET_COMMANDS.site1-commands.&gt;</code></td><td>the site pulls and acks <b>its own</b> commands</td></tr></table>
<p>Nothing else crosses. A site cannot reach another site's consumer, cannot publish commands, and never sees another site's reply subjects. A site1 user with full rights, subscribed to <code>&gt;</code> while site2 worked, saw <b>0</b> site2 messages.</p>
<h3>Cluster routes</h3>
<p>Sites and hubs share the fleet CA, so routes use <code>verify_cert_and_check_known_urls</code>: the peer needs a DNS SAN matching a configured route host, which site certs don't have. A site cert trying to join as a route was rejected on every attempt.</p>
<p>The site LAN has its own limits: <code>edge-app</code> may publish only <code>sites.site1.&gt;</code> (never command requests) and use only its own consumer. <code>site-ops</code> exists for provisioning only.</p>

<h2 id="config">Configuration</h2>
<details open><summary>config/cloud-hub.conf</summary>{code('config/cloud-hub.conf')}</details>
<details><summary>config/hub-accounts.conf (generated)</summary>{code('config/hub-accounts.conf')}</details>
<details><summary>gen-hub-accounts.sh</summary>{code('gen-hub-accounts.sh')}</details>
<details><summary>config/site1-leaf.conf</summary>{code('config/site1-leaf.conf')}</details>
<details><summary>streams/SITE1_UPLINK.json (site domain)</summary>{code('streams/SITE1_UPLINK.json')}</details>
<details><summary>streams/FLEET_UPLINK.json (cloud)</summary>{code('streams/FLEET_UPLINK.json')}</details>
<details><summary>streams/FLEET_COMMANDS.json (cloud)</summary>{code('streams/FLEET_COMMANDS.json')}</details>
<details><summary>streams/site1-commands.consumer.json (cloud)</summary>{code('streams/site1-commands.consumer.json')}</details>
<details><summary>gen-certs.sh</summary>{code('gen-certs.sh')}</details>
<h3>Adding a site</h3>
<pre><code>./gen-certs.sh cloud-hub.example.com,cloud-hub-1,cloud-hub-2,cloud-hub-3 site3   # site3's key stays on site3
./gen-hub-accounts.sh site1 site2 site3 &gt; config/hub-accounts.conf             # reload each hub node
sed 's/site1/site3/g; s/SITE1/SITE3/g' config/site1-leaf.conf &gt; config/site3-leaf.conf
nats --server site3 stream add --config SITE3_UPLINK.json
nats --server cloud consumer add FLEET_COMMANDS --config site3-commands.consumer.json
nats --server cloud stream edit FLEET_UPLINK          # add SITE3_UPLINK source, deliver prefix fleet.deliver.site3</code></pre>

<h2 id="verify">Verification</h2>
<p>Run on nats-server v2.12.1 and nats.go v1.46.1: a 3-node hub cluster and two site servers on localhost, with site1's WAN routed through a TCP proxy that the tests kill and restore. Scripts: <code>test/scenario.sh</code>, <code>scenario-crash.sh</code>, <code>scenario-cluster.sh</code>, <code>scenario-wss.sh</code>.</p>
<table><tr><th>Check</th><th>Observed</th></tr>
<tr><td>WAN cut, site1 publishes 20 more</td><td class="ok">all acked from site disk; cloud stays at 10</td></tr>
<tr><td>WAN restored</td><td class="ok">30 / 30, in order, 0 gaps</td></tr>
<tr><td>During the cut, cloud queues commands with TTL 15 m and 5 s</td><td class="ok">5 s one deleted in the cloud; 15 m one delivered after restore; ack empties the queue</td></tr>
<tr><td>Site server <code>kill -9</code> and a hub node <code>kill -9</code>, both mid-outage</td><td class="ok">40 / 40, 0 gaps; queued command delivered</td></tr>
<tr><td>100,000 backlog; <code>FLEET_UPLINK</code> leader <code>kill -9</code> at 1,334 copied</td><td class="ok">new leader finished 100,000 / 100,000 in 11 s, 0 gaps; command delivered; node rejoined, lag 0</td></tr>
<tr><td>site1 full-rights user asks for <code>site2-commands</code></td><td class="ok">not in site1's account (no responders)</td></tr>
<tr><td>site1 publishes a command request</td><td class="ok">Permissions Violation</td></tr>
<tr><td>site1 full-rights user subscribes to <code>&gt;</code> while site2 works</td><td class="ok">0 site2 messages</td></tr>
<tr><td>valid CA-signed cert, unprovisioned CN</td><td class="ok">Authorization Violation</td></tr>
<tr><td>site cert tries to join the hub cluster</td><td class="ok">rejected every attempt; never a replica</td></tr>
<tr><td>leaf over <code>wss://</code> through an HTTP CONNECT proxy</td><td class="ok">same cert and account: 20 / 20, command delivered, isolation holds</td></tr>
<tr><td>black-holed WAN, ping 20 s / 2</td><td class="ok">link dropped after 61 s</td></tr>
<tr><td>WAN cut / restore</td><td class="ok"><code>$SYS.ACCOUNT.SITE1.DISCONNECT</code> / <code>CONNECT</code> with the cert identity</td></tr></table>

<h3>What the test caught</h3>
<p><b>1. Sourcing over a locked-down leaf.</b> The first permissions draft blocked <code>FLEET_UPLINK</code>: the cloud received 0 messages and logged no error. The source consumer delivers on <code>$JS.S.&lt;rand&gt;</code> and gets its create reply on <code>$JSC.R.&lt;rand&gt;</code> (<code>server/stream.go</code> <code>syncSubject("$JS.S")</code>, <code>server/jetstream_cluster.go</code> <code>infoReplySubject()</code>). Setting <code>external.deliver</code> to a per-site prefix (<code>fleet.deliver.siteN</code>) moves the data onto a subject that can be scoped per leaf.</p>
<p><b>2. Leaves reconnecting around the load balancer.</b> After the simulated WAN cut, site1 reconnected straight to the hub's private IP. A connecting leaf always receives the hub's own leaf URL (<code>server/leafnode.go</code>: <code>s.leafURLsMap[s.leafNodeInfo.IP]++</code>), and <code>no_advertise</code> only affects gossip to cluster routes (<code>server/route.go</code>). The fix is <code>leafnodes.advertise</code> set to the public name.</p>

<p><b>3. Domain prefix intercepted.</b> With per-site accounts, the site's command pulls first failed with "JetStream not enabled for account": the hub answers its own domain prefix <code>$JS.cloud.API.*</code> in every account, before an import applies. Sites therefore use <code>$JS.hub.API</code>, which only the import maps.</p>

<h2 id="behave">Behaviours to plan for</h2>
<ul>
<li><b>Catch-up can start late.</b> A source that kept failing waits out its retry backoff, <code>5s × fails×2</code> capped at 2 m (<code>server/stream.go</code>). Measured 37–58 s between reconnect and the copy starting. Nothing is lost; commands are pulled, so they are unaffected.</li>
<li><b>Ephemeral consumers after a node loss.</b> For about 3 minutes after a hub node died, ~1 in 3 new R1 ephemeral consumers timed out; nats.go's <code>OrderedConsumer</code> then blocked past its own max-wait. Durable R3 consumers were unaffected. Cloud apps should use durable consumers, or create ephemerals with a short timeout and retry. Not root-caused.</li>
<li><b>Config order.</b> In a leaf remote, <code>proxy {{ }}</code> must come after <code>tls {{ }}</code>, or nats-server 2.12.1 rejects the config.</li>
<li><b>Application side.</b> Delivery is at-least-once: dedupe on <code>commandId</code>, set <code>Nats-Msg-Id</code> on uplink publishes, keep the edge <code>expiresAt</code> check and a per-robot sequence guard, and handle publish errors when the site stream is full.</li>
</ul>

<h2 id="gaps">Still needed before production</h2>
<ul>
<li>Load test at fleet size: hundreds of sources on <code>FLEET_UPLINK</code>, and a regional reconnect storm. Tested here with 2 sites.</li>
<li>Power-cut test on real site hardware with <code>sync_interval: always</code>, and its throughput cost there.</li>
<li>Monitoring: per-source last-seen, leaf DISCONNECT events, command queue age, site stream fill.</li>
<li>Certificate rotation runbook and revocation (OCSP or a CRL) on the leaf and WebSocket listeners.</li>
<li>Pin and retest the nats-server version you deploy.</li>
</ul>
</main></body></html>'''
(R/'docs/index.html').write_text(page)
print(len(page))
