#!/bin/bash
# Test bed: 3-node cloud hub cluster + 2 site leaves, all on localhost.
#   hub N (1..3): client 42N2, leaf 74N2, route 62N2, websocket 84N3 (hub1: 4212/7412/6212/8413)
#   site1 -> hubs through a killable "WAN" proxy (174N2 -> 74N2)
#   site2 -> hubs directly
# Configs are generated from ../config with only ports, paths and host names
# changed, so the tests exercise the real files.
set -u
R=$(cd "$(dirname "$0")/.." && pwd); T=$R/test/run
command -v nats-server >/dev/null || { echo "nats-server not on PATH (brew install nats-server)"; exit 1; }
# test certs: hub cert valid for localhost (sites dial it, routes use it too)
[ -f $R/certs/leaf-site2.crt ] || (cd $R && ./gen-certs.sh localhost site1 site2 >/dev/null 2>&1)
[ -x $R/test/harness ] || (cd $R/test && go build -o harness .) || exit 1
rm -rf $T; mkdir -p $T
export CLOUD_APP_PASS=cloudpw SYS_PASS=syspw EDGE_APP_PASS=edgepw SITE_OPS_PASS=opspw
cp $R/config/hub-accounts.conf $T/
WAN="17412:7412 17422:7422 17432:7432"   # site1 WAN proxy: local:hub-leaf-port

python3 - "$R" "$T" <<'P'
import sys, re
R, T = sys.argv[1], sys.argv[2]
hub = open(f"{R}/config/cloud-hub.conf").read()
for n in (1, 2, 3):
    c = hub.replace("./data", f"{T}/data/hub{n}").replace("./certs", f"{R}/certs")
    c = c.replace("server_name: $HUB_NAME", f"server_name: cloud-hub-{n}")
    c = c.replace("listen: 0.0.0.0:4222", f"listen: 127.0.0.1:42{n}2")
    c = c.replace("listen: 0.0.0.0:7422", f"listen: 127.0.0.1:74{n}2")
    c = c.replace("listen: 0.0.0.0:6222", f"listen: 127.0.0.1:62{n}2")
    c = c.replace('listen: "0.0.0.0:443"', f'listen: "127.0.0.1:84{n}3"')
    c = c.replace('advertise: "cloud-hub:7422"', f'advertise: "localhost:174{n}2"')
    c = re.sub(r'nats-route://cloud-hub-(\d):6222', lambda m: f"nats-route://localhost:62{m.group(1)}2", c)
    open(f"{T}/cloud{n}.conf", "w").write(c)
for s, port, hubs in (("site1", 4223, "174"), ("site2", 4224, "74")):
    c = open(f"{R}/config/{s}-leaf.conf").read()
    c = c.replace("./data", f"{T}/data").replace("./certs", f"{R}/certs")
    c = c.replace("listen: 0.0.0.0:4222", f"listen: 127.0.0.1:{port}")
    urls = ", ".join(f'"tls://localhost:{hubs}{n}2"' for n in (1, 2, 3))
    c = c.replace('urls: [ "tls://cloud-hub:7422" ]', f"urls: [ {urls} ]")
    open(f"{T}/{s}.conf", "w").write(c)
P

for c in cloud1 cloud2 cloud3 site1 site2; do
  nats-server -c $T/$c.conf -l $T/$c.log >/dev/null 2>&1 </dev/null & echo $! > $T/$c.pid
done
python3 $R/test/wanproxy.py $WAN >/dev/null 2>&1 </dev/null & echo $! > $T/wan.pid

# wait (max 40s) for JetStream meta leader + both leaves attached
for i in $(seq 40); do
  ok=$(grep -l "JetStream cluster new metadata leader" $T/cloud*.log 2>/dev/null | wc -l)
  lv=$(cat $T/site1.log $T/site2.log 2>/dev/null | grep -c "Leafnode connection created for account")
  [ "$ok" -ge 1 ] && [ "$lv" -ge 2 ] && break; sleep 1
done
echo "hub cluster: $(grep -h 'JetStream cluster new metadata leader' $T/cloud*.log | tail -1 | sed 's/.*leader: //') is meta leader; leaves attached: $lv"
