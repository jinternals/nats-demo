#!/bin/bash
# Failure modes the MQTT repo documents, replayed against the NATS design:
# outage + site server crash + hub restart, then restore.
cd "$(dirname "$0")"; H=./harness; T=run
export CLOUD_APP_PASS=cloudpw SYS_PASS=syspw EDGE_APP_PASS=edgepw SITE_OPS_PASS=opspw
restart() { kill -9 $(cat $T/$1.pid); sleep 1; nats-server -c $T/$1.conf -l $T/$1.log >/dev/null 2>&1 & echo $! > $T/$1.pid; sleep 3; }
pkill nats-server; pkill -f wanproxy; sleep 1; ./run.sh >/dev/null
$H setup >/dev/null && $H edge-pub site1 1 10; sleep 2
$H sys-watch 60 > $T/sys.txt &
echo "=== CUT site1 WAN ==="; kill $(cat $T/wan.pid); sleep 3
$H edge-pub site1 11 30
$H cloud-cmd site1 arm-01 cmd-A 15m
$H source-info
echo "=== kill -9 site1 leaf mid-outage, restart ==="; restart site1
$H edge-pub site1 31 40
echo "=== kill -9 cloud hub node 1 mid-outage, restart ==="; restart cloud1
$H cloud-count | head -2
sleep 2
echo "=== RESTORE site1 WAN ==="; n0=$(grep -c "Leafnode connection created for account" $T/site1.log); python3 wanproxy.py 17412:7412 17422:7422 17432:7432 >/dev/null 2>&1 & echo $! > $T/wan.pid
for i in $(seq 30); do [ $(grep -c "Leafnode connection created for account" $T/site1.log) -gt $n0 ] && break; sleep 1; done; sleep 3; echo "site1 reconnected after ~${i}s"
$H wait-uplink site1 40 150   # the source may first sit out its retry backoff (<= 2m)
$H source-info
$H cloud-count
$H edge-pull site1 site1
wait
echo "=== hub system events ==="; cat $T/sys.txt
