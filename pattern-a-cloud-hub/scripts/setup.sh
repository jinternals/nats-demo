#!/bin/sh
# Creates the streams and consumers. Runs once in nats-box on `docker compose up`.
# Safe to re-run: anything that already exists is left alone.
set -eu
: "${SITES:?set SITES, e.g. \"site1 site2\"}"
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
ensure_stream() {   # ensure_stream "<connection flags>" NAME FILE
  nats $1 stream info "$2" >/dev/null 2>&1 && return
  retry nats $1 stream add --config "$3"
  echo "created stream $2"
}

for s in $SITES; do ensure_stream "$(site "$s")" "$s-outbox" "/config/generated/streams/$s-outbox.json"; done
ensure_stream "$HUB" cloud-outbox /config/streams/cloud-outbox.json
ensure_stream "$HUB" cloud-inbox /config/generated/streams/cloud-inbox.json
for s in $SITES; do
  nats $HUB consumer info cloud-outbox "$s-controller" >/dev/null 2>&1 && continue
  retry nats $HUB consumer add cloud-outbox --config "/config/generated/streams/$s-controller.json"
  echo "created consumer cloud-outbox/$s-controller"
done
echo "setup complete"
