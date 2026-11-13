# OpenLDAP for dcdash integration tests (generic LDAP mode). NOT for production use.
FROM debian:trixie-slim
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends slapd ldap-utils openssl \
 && rm -rf /var/lib/apt/lists/*
COPY slapd.conf data.ldif entrypoint.sh /it/
ENTRYPOINT ["/bin/sh", "/it/entrypoint.sh"]
