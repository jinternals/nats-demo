#!/bin/sh
# Creates the streams and consumers. Runs once in nats-box on `docker compose up`.
# Safe to re-run: anything that already exists is left alone.
set -eu
SITES="site1 site2"
HUB="--server tls://cloud-hub-1:4222,tls://cloud-hub-2:4222,tls://cloud-hub-3:4222 --tlsca /certs/ca.crt --user cloud-app --password $CLOUD_APP_PASS"
site() { echo "--server nats://$1:4222 --user site-ops --password $SITE_OPS_PASS"; }

# Retry while servers start and the hub cluster elects a JetStream leader.
retry() {
  n=0
  until "$@" >/dev/null 2>&1; do
    n=$((n + 1)); [ $n -ge 60 ] && { echo "failed: $*" >&2; "$@"; exit 1; }
    sleep 1
  done
}
ensure_stream() {   # ensure_stream "<conn flags>" NAME
  nats $1 stream info "$2" >/dev/null 2>&1 && return
  retry nats $1 stream add --config "/config/streams/$2.json"
  echo "created stream $2"
}

for s in $SITES; do ensure_stream "$(site "$s")" "$s-uplink"; done
ensure_stream "$HUB" fleet-commands
ensure_stream "$HUB" fleet-uplink
for s in $SITES; do
  nats $HUB consumer info fleet-commands "$s-commands" >/dev/null 2>&1 && continue
  retry nats $HUB consumer add fleet-commands --config "/config/streams/$s-commands.json"
  echo "created consumer fleet-commands/$s-commands"
done
echo "setup complete"
