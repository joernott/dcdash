#!/bin/sh
# Provisions the test domain ad.dcdash.test on the first start and runs Samba in the
# foreground. NOT for production use: all passwords are test-only. Extended attributes
# (the NT ACLs of the sysvol share) are kept in a TDB (xattr_tdb), so the container
# needs no extra capabilities.
set -eu

REALM=AD.DCDASH.TEST
IP=${SAMBA_IP:-10.77.0.88}

if [ ! -f /var/lib/samba/private/sam.ldb ]; then
    samba-tool domain provision \
        --realm="$REALM" --domain=DCDASH --server-role=dc \
        --dns-backend=SAMBA_INTERNAL --host-name=dc1 --host-ip="$IP" \
        --adminpass='Admin-test-only1' \
        --option="dns forwarder = 127.0.0.1" \
        --option="vfs objects = dfs_samba4 acl_xattr xattr_tdb" \
        --option="ldap server require strong auth = yes"
    # A lockout policy, so that the tests can lock and unlock accounts, and no minimum
    # password age, so that they can change a password twice.
    samba-tool domain passwordsettings set --account-lockout-threshold=5 \
        --account-lockout-duration=30 --reset-account-lockout-after=30 --min-pwd-age=0
    # dcdash's service account, a managed OU with a user and a group, and an
    # AD-integrated zone besides the domain's own.
    samba-tool ou create OU=dcdash
    samba-tool user create svc-dcdash 'Svc-test-only1' --description="dcdash service account"
    samba-tool group addmembers "Domain Admins" svc-dcdash
    samba-tool user create alice 'Alice-test-only1' --userou=OU=dcdash \
        --given-name=Alice --surname=Example --mail-address=alice@ad.dcdash.test
    samba-tool group add lab-admins --groupou=OU=dcdash --description="Lab administrators"
    samba-tool group addmembers lab-admins alice
    cp /var/lib/samba/private/krb5.conf /etc/krb5.conf
    # Samba's self-signed certificate may have a negative serial number, which Go
    # rejects; Samba only generates one if none exists.
    TLS=/var/lib/samba/private/tls
    mkdir -p "$TLS"
    openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 3650 \
        -subj "/CN=dc1.ad.dcdash.test" -addext "subjectAltName=DNS:dc1.ad.dcdash.test,IP:$IP" \
        -keyout "$TLS/key.pem" -out "$TLS/cert.pem" 2>/dev/null
    chmod 600 "$TLS/key.pem"
    cp "$TLS/cert.pem" "$TLS/ca.pem"
fi

# The second zone needs the running server (DNS RPC); the healthcheck waits for it.
if [ ! -f /var/lib/samba/zone-created ]; then
    (
        for _ in $(seq 60); do
            sleep 2
            if samba-tool dns zonecreate 127.0.0.1 lab.dcdash.test -U Administrator \
                --password='Admin-test-only1' >/dev/null 2>&1; then
                touch /var/lib/samba/zone-created
                break
            fi
        done
    ) &
fi

exec samba --foreground --no-process-group --debug-stdout
