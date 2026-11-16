#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
# Creates the system user and group dcdash.
set -e
if ! getent group dcdash >/dev/null; then
  groupadd --system dcdash
fi
if ! getent passwd dcdash >/dev/null; then
  useradd --system --gid dcdash --home-dir /var/lib/dcdash --no-create-home \
    --shell /usr/sbin/nologin --comment "dcdash data center dashboard" dcdash
fi
