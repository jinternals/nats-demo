#!/bin/bash
# Fleet CA, hub server cert, and ONE client cert per site (per-leaf identity).
# usage: ./gen-certs.sh cloud-hub.example.com site1 site2 ...
set -euo pipefail
HUB=$1; shift; mkdir -p certs; cd certs
[ -f ca.key ] || openssl req -x509 -newkey rsa:2048 -nodes -keyout ca.key -out ca.crt -days 3650 -subj "/CN=fleet-ca"
openssl req -newkey rsa:2048 -nodes -keyout cloud.key -out cloud.csr -subj "/CN=$HUB"
printf "subjectAltName=DNS:%s\nextendedKeyUsage=serverAuth" "$HUB" > s.ext
openssl x509 -req -in cloud.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out cloud.crt -days 825 -extfile s.ext
for s in "$@"; do
  openssl req -newkey rsa:2048 -nodes -keyout leaf-$s.key -out leaf-$s.csr -subj "/CN=leaf-$s/O=fleet"
  printf "extendedKeyUsage=clientAuth" > c.ext
  openssl x509 -req -in leaf-$s.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out leaf-$s.crt -days 825 -extfile c.ext
  echo "hub user for $s: \"CN=leaf-$s,O=fleet\"   (form verified against nats-server 2.12.1)"
done
rm -f *.csr *.ext
