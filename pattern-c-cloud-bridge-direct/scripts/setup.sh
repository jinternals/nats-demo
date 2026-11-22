#!/bin/sh
# Creates per-site streams (<site>-outbox, <site>-inbox) and controller consumer
# for Pattern B (Direct Site-to-Kafka). Runs once in nats-box on `make up-direct`.
set -eu
: "${SITES:?set SITES, e.g. \"site1 site2\"}"
site() { echo "--server nats://$1:4222 --user site-ops --password $SITE_OPS_PASS"; }

retry() {
  n=0
  until "$@" >/dev/null 2>&1; do
    n=$((n + 1)); [ $n -ge 60 ] && { echo "failed: $*" >&2; "$@"; exit 1; }
    sleep 1
  done
}

ensure_stream() {
  nats $1 stream info "$2" >/dev/null 2>&1 && return
  retry nats $1 stream add --config "$3"
  echo "created stream $2"
}

for s in $SITES; do
  ensure_stream "$(site "$s")" "$s-outbox" "/config/generated/streams/$s-outbox.json"
  ensure_stream "$(site "$s")" "$s-inbox"  "/config/generated/streams/$s-inbox.json"
  if ! nats $(site "$s") consumer info "$s-inbox" "$s-controller" >/dev/null 2>&1; then
    retry nats $(site "$s") consumer add "$s-inbox" --config "/config/generated/streams/$s-controller.json"
    echo "created consumer $s-inbox/$s-controller"
  fi
done
echo "setup complete"
