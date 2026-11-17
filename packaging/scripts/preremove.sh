#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
# Stops the service before the package is removed (not on upgrades).
set -e
case "$1" in
  remove|0)
    if [ -d /run/systemd/system ]; then
      systemctl disable --now dcdash >/dev/null 2>&1 || true
    fi
    ;;
esac
