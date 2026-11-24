#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
# rpm only. dcdash 0.3 and earlier shipped /etc/dcdash/config.yaml; newer packages ship the
# directory /etc/dcdash/config.d instead. On the upgrade, rpm saves the changed old file as
# config.yaml.rpmsave. Restore it: dcdash reads it until it is moved into the directory.
set -e
old=/etc/dcdash/config.yaml
if [ -f "$old.rpmsave" ] && [ ! -e "$old" ]; then
  mv "$old.rpmsave" "$old"
  echo "dcdash: restored $old; move it into /etc/dcdash/config.d (see docs/configuration.md)"
fi
