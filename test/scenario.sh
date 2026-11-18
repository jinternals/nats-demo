#!/bin/bash
cd "$(dirname "$0")"; H=./harness; T=run
pkill nats-server; pkill -f wanproxy; sleep 1; ./run.sh
$H setup && $H edge-pub site1 1 10 && $H edge-pub site2 1 5 && sleep 2 && $H cloud-count
echo "=== CUT site1 WAN ==="; kill $(cat $T/wan.pid); sleep 3
$H edge-pub site1 11 30
$H cloud-cmd site1 arm-01 cmd-fresh 15m
$H cloud-cmd site1 arm-01 cmd-stale 5s
$H edge-pull site1 site1
sleep 7; $H cloud-count
echo "=== RESTORE site1 WAN ==="; n0=$(grep -c "Leafnode connection created for account" $T/site1.log); python3 wanproxy.py 17412:7412 17422:7422 17432:7432 >/dev/null 2>&1 & echo $! > $T/wan.pid
for i in $(seq 30); do [ $(grep -c "Leafnode connection created for account" $T/site1.log) -gt $n0 ] && break; sleep 1; done; sleep 3; echo "site1 reconnected after ~${i}s"
$H wait-uplink site1 30 150
$H cloud-count
$H edge-pull site1 site1
$H edge-pull site1 site1
echo "=== isolation ==="; $H edge-pull site1 site2; $H edge-inject site1
$H ops-steal site1 site2
$H cloud-cmd site2 arm-11 cmd-s2 15m
timeout 20 $H edge-pull site2 site2
echo "=== cross-site sniff: site1 full-rights user listens on '>' while site2 works ==="
$H ops-sniff site1 12 > $T/sniff.txt &
sleep 2; $H edge-pub site2 6 25 >/dev/null; $H cloud-cmd site2 arm-11 cmd-s2b 15m >/dev/null; timeout 20 $H edge-pull site2 site2; sleep 3
wait; cat $T/sniff.txt
