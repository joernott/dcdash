#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
# Reloads systemd. The user, the configuration, the encryption key and the data in
# /var/lib/dcdash are kept; remove them by hand if they are no longer needed.
set -e
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload >/dev/null 2>&1 || true
fi
