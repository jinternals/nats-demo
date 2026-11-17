#!/bin/bash
cd /home/claude/nats-edge/test; H=./harness; T=run
pkill nats-server; pkill -f wanproxy; sleep 1; ./run.sh
$H setup && $H edge-pub site1 1 10 && $H edge-pub site2 1 5 && sleep 2 && $H cloud-count
echo "=== CUT site1 WAN ==="; kill $(cat $T/wan.pid); sleep 3
$H edge-pub site1 11 30
$H cloud-cmd site1 arm-01 cmd-fresh 15m
$H cloud-cmd site1 arm-01 cmd-stale 5s
$H edge-pull site1 site1
sleep 7; $H cloud-count
echo "=== RESTORE site1 WAN ==="; python3 wanproxy.py 17422 7422 & echo $! > $T/wan.pid; sleep 6
$H cloud-count
$H edge-pull site1 site1
$H edge-pull site1 site1
echo "=== isolation ==="; $H edge-pull site1 site2; $H edge-inject site1
$H ops-steal site1 site2
$H cloud-cmd site2 arm-11 cmd-s2 15m
timeout 20 $H edge-pull site2 site2
