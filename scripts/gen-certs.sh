#!/bin/sh
# Dev certificates: a CA, one hub cert, one client cert per site.
#   usage: scripts/gen-certs.sh OUT_DIR HUB_DNS_NAMES site1 site2 ...
#   e.g.   scripts/gen-certs.sh certs cloud-hub,cloud-hub-1,cloud-hub-2,cloud-hub-3,localhost site1 site2
#
# The hub cert lists every DNS name sites and peers use, and is valid as both
# server and client (hubs connect to each other). A site cert's subject,
# CN=<site>,O=fleet, is the user name the hub maps it to.
set -eu
[ $# -ge 3 ] || { echo "usage: $0 OUT_DIR HUB_DNS_NAMES site1 [site2 ...]" >&2; exit 1; }
out=$1; names=$2; shift 2
mkdir -p "$out"; cd "$out"

[ -f ca.key ] && [ -f ca.crt ] || openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
  -keyout ca.key -out ca.crt -subj "/CN=fleet-dev-ca" 2>/dev/null

san=$(echo "$names" | tr ',' '\n' | sed 's/^/DNS:/' | paste -sd, -)
printf 'subjectAltName=%s\nextendedKeyUsage=serverAuth,clientAuth\n' "$san" > hub.ext
openssl req -newkey rsa:2048 -nodes -keyout hub.key -out hub.csr -subj "/CN=cloud-hub" 2>/dev/null
openssl x509 -req -in hub.csr -CA ca.crt -CAkey ca.key -CAcreateserial -days 825 \
  -out hub.crt -extfile hub.ext 2>/dev/null

printf 'extendedKeyUsage=clientAuth\n' > site.ext
for s in "$@"; do
  openssl req -newkey rsa:2048 -nodes -keyout "$s.key" -out "$s.csr" -subj "/CN=$s/O=fleet" 2>/dev/null
  openssl x509 -req -in "$s.csr" -CA ca.crt -CAkey ca.key -CAcreateserial -days 825 \
    -out "$s.crt" -extfile site.ext 2>/dev/null
done
rm -f ./*.csr ./*.ext ca.srl
chmod 644 ./*.crt ./*.key   # read by the nats user inside the containers (dev only)
echo "certs written to $out: ca, hub, $*"
