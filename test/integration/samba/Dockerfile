# Samba AD domain controller for the directory integration and e2e tests
# (docs/adr/0010-active-directory.md). NOT for production use: all passwords are test-only.
FROM debian:trixie-slim
RUN apt-get update -q \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y -q --no-install-recommends \
       samba samba-ad-dc samba-ad-provision samba-dsdb-modules samba-vfs-modules winbind \
       ldb-tools bind9-dnsutils ca-certificates openssl \
    && rm -rf /var/lib/apt/lists/* \
    && rm -f /etc/samba/smb.conf
COPY entrypoint.sh /entrypoint.sh
ENTRYPOINT ["/bin/sh", "/entrypoint.sh"]
