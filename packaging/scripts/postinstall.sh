#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
# Creates the encryption key on the first installation and reloads systemd.
# The service is neither enabled nor started: the configuration needs editing first.
set -e
key=/etc/dcdash/encryption.key
if [ ! -e "$key" ]; then
  (umask 027 && /usr/bin/dcdash gen-key > "$key")
  chown root:dcdash "$key"
  chmod 0640 "$key"
fi
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload >/dev/null 2>&1 || true
  if systemctl is-active --quiet dcdash; then
    systemctl restart dcdash || true
  fi
fi
if [ ! -e /var/lib/dcdash/dcdash.db ]; then
  cat <<'MSG'
dcdash is installed. Next steps:
  1. Edit the files in /etc/dcdash/config.d (TLS, LDAP, roles, modules).
  2. dcdash config validate
  3. systemctl enable --now dcdash
Back up /etc/dcdash/encryption.key separately from /var/lib/dcdash.
See https://github.com/joernott/dcdash/blob/main/docs/installation.md
MSG
fi
if [ -f /etc/dcdash/config.yaml ]; then
  cat <<'MSG'
dcdash reads /etc/dcdash/config.yaml as before. The configuration is now a directory,
/etc/dcdash/config.d, with example files. To switch, move your settings into the files
there (or move config.yaml into the directory and delete the examples it duplicates), then
remove /etc/dcdash/config.yaml and run: dcdash config validate
MSG
fi
