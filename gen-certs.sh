#!/bin/bash
# Fleet CA, hub server cert, and ONE client cert per site (per-leaf identity).
# usage: ./gen-certs.sh HUB_NAMES site1 site2 ...
#   HUB_NAMES: comma-separated DNS names for the hub cert, e.g.
#   cloud-hub.example.com,cloud-hub-1,cloud-hub-2,cloud-hub-3
#   (the public/LB name that sites dial, plus every route host)
set -euo pipefail
HUB=$1; shift; mkdir -p certs; cd certs
[ -f ca.key ] || openssl req -x509 -newkey rsa:2048 -nodes -keyout ca.key -out ca.crt -days 3650 -subj "/CN=fleet-ca"
openssl req -newkey rsa:2048 -nodes -keyout cloud.key -out cloud.csr -subj "/CN=${HUB%%,*}"
SAN=$(tr ',' '\n' <<<"$HUB" | sed 's/^/DNS:/' | paste -sd, -)
# clientAuth too: hub nodes are TLS clients of each other on routes
printf "subjectAltName=%s\nextendedKeyUsage=serverAuth,clientAuth" "$SAN" > s.ext
openssl x509 -req -in cloud.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out cloud.crt -days 825 -extfile s.ext
for s in "$@"; do
  openssl req -newkey rsa:2048 -nodes -keyout leaf-$s.key -out leaf-$s.csr -subj "/CN=leaf-$s/O=fleet"
  printf "extendedKeyUsage=clientAuth" > c.ext
  openssl x509 -req -in leaf-$s.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out leaf-$s.crt -days 825 -extfile c.ext
  echo "hub user for $s: \"CN=leaf-$s,O=fleet\"   (form verified against nats-server 2.12.1)"
done
rm -f *.csr *.ext
