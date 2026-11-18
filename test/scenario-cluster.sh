#!/bin/bash
# 3-node hub: lose the FLEET_UPLINK stream leader while site1 is draining a
# backlog after an outage; then check nothing was lost and the node rejoins.
# Also: a site cert must not be able to join the hub cluster.
cd "$(dirname "$0")"; H=./harness; T=run; R=$(cd .. && pwd)
WAN="17412:7412 17422:7422 17432:7432"
export CLOUD_APP_PASS=cloudpw SYS_PASS=syspw EDGE_APP_PASS=edgepw SITE_OPS_PASS=opspw
pkill nats-server; pkill -f wanproxy; sleep 1; ./run.sh
N0=100000
$H setup >/dev/null && $H replicas FLEET_UPLINK FLEET_COMMANDS
echo "=== CUT site1 WAN; site1 builds a backlog of $N0 ==="; kill $(cat $T/wan.pid); sleep 3
$H edge-pub site1 1 $N0
$H cloud-cmd site1 arm-01 cmd-C 15m
echo "=== RESTORE, and kill -9 the FLEET_UPLINK leader as soon as site1 is back ==="
n0=$(grep -c "Leafnode connection created for account" $T/site1.log); python3 wanproxy.py $WAN >/dev/null 2>&1 </dev/null & echo $! > $T/wan.pid
for i in $(seq 30); do [ $(grep -c "Leafnode connection created for account" $T/site1.log) -gt $n0 ] && break; sleep 0.5; done
# wait until the copy is under way (0 < msgs < $N0), then kill the leader
# (the source may first sit out its retry backoff: up to 2m, server/stream.go)
t0=$(date +%s)
for i in $(seq 3000); do
  m=$($H replicas FLEET_UPLINK | sed 's/.* msgs=\([0-9]*\).*/\1/'); [ "$m" -gt 0 ] && break; sleep 0.05
done
echo "copy started t+$(( $(date +%s)-t0 ))s after site1 reconnected"
L=$($H leader FLEET_UPLINK); N=${L##*-}
kill -9 $(cat $T/cloud$N.pid); echo "killed $L (FLEET_UPLINK leader) with $m/$N0 copied"
$H wait-uplink site1 $N0 150
$H replicas FLEET_UPLINK FLEET_COMMANDS
$H cloud-count
timeout 20 $H edge-pull site1 site1
echo "=== restart $L ==="
nats-server -c $T/cloud$N.conf -l $T/cloud$N.log >/dev/null 2>&1 </dev/null & echo $! > $T/cloud$N.pid; sleep 10
$H replicas FLEET_UPLINK FLEET_COMMANDS
echo "=== a site cert tries to join the hub cluster as a route ==="
sed "s#server_name: cloud-hub-1#server_name: rogue-route#; s#127.0.0.1:4212#127.0.0.1:4299#; s#127.0.0.1:7412#127.0.0.1:7499#; s#127.0.0.1:6212#127.0.0.1:6299#; s#127.0.0.1:8413#127.0.0.1:8499#; s#$T/data/hub1#$T/data/rogue#; s#$R/certs/cloud.crt#$R/certs/leaf-site1.crt#g; s#$R/certs/cloud.key#$R/certs/leaf-site1.key#g" $T/cloud1.conf > $T/rogue.conf
(timeout 8 nats-server -c $T/rogue.conf -l $T/rogue.log >/dev/null 2>&1 </dev/null; true)
echo "hub-side route rejections (Authentication Failure): $(grep -h 'Router connection closed: Authentication Failure' $T/cloud*.log | wc -l)"
$H replicas FLEET_UPLINK | grep -q rogue && echo "ROGUE JOINED JETSTREAM" || echo "rogue never became a replica"
pkill nats-server; pkill -f wanproxy
