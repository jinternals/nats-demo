#!/bin/bash
# Spins up hub + 2 leaves; site1's WAN goes through a killable proxy.
set -u
R=/home/claude/nats-edge; T=$R/test/run; rm -rf $T; mkdir -p $T
export CLOUD_APP_PASS=cloudpw LEAF_SITE1_PASS=leaf1pw LEAF_SITE2_PASS=leaf2pw SYS_PASS=syspw EDGE_APP_PASS=edgepw SITE_OPS_PASS=opspw
sed "s#\./data#$T/data#; s#\./certs#$R/certs#g; s#advertise: "cloud-hub:7422"#advertise: "localhost:17422"#" $R/config/cloud-hub.conf > $T/cloud.conf
sed "s#\./data#$T/data#; s#\./certs#$R/certs#g; s#0.0.0.0:4222#127.0.0.1:4223#; s#cloud-hub:7422#localhost:17422#" $R/config/site1-leaf.conf > $T/site1.conf
sed "s#\./data#$T/data#; s#\./certs#$R/certs#g; s#0.0.0.0:4222#127.0.0.1:4224#; s#cloud-hub:7422#localhost:7422#" $R/config/site2-leaf.conf > $T/site2.conf
for c in cloud site1 site2; do nats-server -c $T/$c.conf -l $T/$c.log >/dev/null 2>&1 & echo $! > $T/$c.pid; done
python3 $R/test/wanproxy.py 17422 7422 >/dev/null 2>&1 & echo $! > $T/wan.pid
sleep 4; grep -h -iE "error|leafnode connection created" $T/*.log | head
