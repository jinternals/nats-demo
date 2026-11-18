#!/bin/bash
# Spins up hub + 2 leaves; site1's WAN goes through a killable proxy.
set -u
R=$(cd "$(dirname "$0")/.." && pwd); T=$R/test/run
command -v nats-server >/dev/null || { echo "nats-server not on PATH (brew install nats-server)"; exit 1; }
# test certs: hub cert must be valid for "localhost"
[ -f $R/certs/leaf-site2.crt ] || (cd $R && ./gen-certs.sh localhost site1 site2 >/dev/null 2>&1)
[ -x $R/test/harness ] || (cd $R/test && go build -o harness .) || exit 1
 rm -rf $T; mkdir -p $T
export CLOUD_APP_PASS=cloudpw LEAF_SITE1_PASS=leaf1pw LEAF_SITE2_PASS=leaf2pw SYS_PASS=syspw EDGE_APP_PASS=edgepw SITE_OPS_PASS=opspw
sed "s#\./data#$T/data#; s#\./certs#$R/certs#g; s#advertise: \"cloud-hub:7422\"#advertise: \"localhost:17422\"#; s#listen: \"0.0.0.0:443\"#listen: \"127.0.0.1:8443\"#" $R/config/cloud-hub.conf > $T/cloud.conf
sed "s#\./data#$T/data#; s#\./certs#$R/certs#g; s#0.0.0.0:4222#127.0.0.1:4223#; s#cloud-hub:7422#localhost:17422#" $R/config/site1-leaf.conf > $T/site1.conf
sed "s#\./data#$T/data#; s#\./certs#$R/certs#g; s#0.0.0.0:4222#127.0.0.1:4224#; s#cloud-hub:7422#localhost:7422#" $R/config/site2-leaf.conf > $T/site2.conf
for c in cloud site1 site2; do nats-server -c $T/$c.conf -l $T/$c.log >/dev/null 2>&1 & echo $! > $T/$c.pid; done
python3 $R/test/wanproxy.py 17422 7422 >/dev/null 2>&1 & echo $! > $T/wan.pid
# wait (max 30s) until both leaves are attached to the hub
for i in $(seq 30); do
  [ "$(grep -c 'Leafnode connection created for account' $T/site1.log $T/site2.log 2>/dev/null | awk -F: '{s+=$2} END {print s}')" -ge 2 ] && break; sleep 1
done
grep -h "Leafnode connection created for account" $T/site*.log | wc -l | xargs echo "leaves attached:"
