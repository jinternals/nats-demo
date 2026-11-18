#!/bin/bash
# Leaf over WebSocket (wss://, port 8443 in the test) through an HTTP CONNECT
# proxy, with the same per-site client certificate. Uses run.sh's generated
# configs, then switches site1's remote from tls:// to wss:// + proxy.
cd "$(dirname "$0")"; H=./harness; T=run; R=$(cd .. && pwd)
export CLOUD_APP_PASS=cloudpw SYS_PASS=syspw EDGE_APP_PASS=edgepw SITE_OPS_PASS=opspw
pkill nats-server; pkill -f wanproxy; pkill -f connectproxy; sleep 1
./run.sh >/dev/null; pkill nats-server; pkill -f wanproxy; sleep 1; rm -rf $T/data
python3 - "$T" <<'P'
import sys; T=sys.argv[1]
s=open(f"{T}/site1.conf").read()
import re
s=re.sub(r'urls: \[ "tls://localhost:17412".*?\]','urls: [ "wss://localhost:8413", "wss://localhost:8423", "wss://localhost:8433" ]',s)
i=s.index('key_file:',s.index('remotes')); j=s.index('}',i)+1
s=s[:j]+'\n      proxy { url: "http://127.0.0.1:3128" }'+s[j:]
open(f"{T}/site1.conf","w").write(s)
P
python3 connectproxy.py > $T/proxy.log 2>&1 </dev/null & echo $! > $T/proxy.pid
for c in cloud1 cloud2 cloud3 site1 site2; do nats-server -c $T/$c.conf -l $T/$c.log >/dev/null 2>&1 </dev/null & echo $! > $T/$c.pid; done
for i in $(seq 40); do grep -q "lid_ws.*created for account" $T/site1.log && break; sleep 1; done
echo "proxy saw: $(sort -u $T/proxy.log | tr '\n' ' ')"; grep -h "lid_ws.*created for account" $T/site1.log | tail -1 | sed 's/.*- lid_ws/site1 leaf: lid_ws/'
$H setup >/dev/null && $H edge-pub site1 1 20 && $H wait-uplink site1 20 150 && $H cloud-count | tail -1
$H cloud-cmd site1 arm-01 ws-cmd 15m && $H edge-pull site1 site1
$H ops-steal site1 site2; $H edge-inject site1
pkill nats-server; pkill -f connectproxy
