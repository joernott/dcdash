#!/bin/sh
# Prepares certificate and database, then runs slapd in the foreground.
set -eu
mkdir -p /certs /var/lib/ldap-it
if [ ! -f /certs/cert.pem ]; then
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 30 \
    -subj "/CN=localhost" -addext "subjectAltName=DNS:localhost,DNS:openldap,IP:127.0.0.1" \
    -keyout /certs/key.pem -out /certs/cert.pem 2>/dev/null
fi
if [ ! -f /var/lib/ldap-it/data.mdb ]; then
  slapadd -f /it/slapd.conf -l /it/data.ldif
fi
exec slapd -d 0 -h "ldap:/// ldaps:///" -f /it/slapd.conf
