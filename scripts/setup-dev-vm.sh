#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Provision a Debian 13 development machine for dcdash.
# Installs: base build tools, Go, Node.js, golangci-lint (versions pinned in the
# repository root: .go-version, .node-version, .golangci-lint-version) and Docker.
#
# Usage: sudo scripts/setup-dev-vm.sh [<user to add to the docker group>]
set -euo pipefail

if [[ $EUID -ne 0 ]]; then
  echo "run as root (sudo $0)" >&2
  exit 1
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_VERSION="$(tr -d '[:space:]' < "$REPO_ROOT/.go-version")"
NODE_VERSION="$(tr -d '[:space:]' < "$REPO_ROOT/.node-version")"
GOLANGCI_VERSION="$(tr -d '[:space:]' < "$REPO_ROOT/.golangci-lint-version")"
DEV_USER="${1:-${SUDO_USER:-}}"

case "$(uname -m)" in
  x86_64) ARCH=amd64; NODE_ARCH=x64 ;;
  aarch64) ARCH=arm64; NODE_ARCH=arm64 ;;
  *) echo "unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

echo "==> Base packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -q
apt-get install -y -q build-essential make git curl ca-certificates gnupg xz-utils unzip jq rsync

echo "==> Go ${GO_VERSION}"
if [[ "$(/usr/local/go/bin/go version 2>/dev/null | awk '{print $3}')" != "go${GO_VERSION}" ]]; then
  curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${ARCH}.tar.gz" -o /tmp/go.tgz
  rm -rf /usr/local/go
  tar -C /usr/local -xzf /tmp/go.tgz
  rm /tmp/go.tgz
fi
ln -sf /usr/local/go/bin/go /usr/local/bin/go
ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt

echo "==> Node.js ${NODE_VERSION}"
if [[ "$(/usr/local/bin/node --version 2>/dev/null)" != "v${NODE_VERSION}" ]]; then
  NODE_DIR="node-v${NODE_VERSION}-linux-${NODE_ARCH}"
  curl -fsSL "https://nodejs.org/dist/v${NODE_VERSION}/${NODE_DIR}.tar.xz" -o /tmp/node.txz
  curl -fsSL "https://nodejs.org/dist/v${NODE_VERSION}/SHASUMS256.txt" -o /tmp/node.sums
  (cd /tmp && grep " ${NODE_DIR}.tar.xz\$" node.sums | sed "s/${NODE_DIR}.tar.xz/node.txz/" | sha256sum -c -)
  rm -rf /usr/local/lib/nodejs
  mkdir -p /usr/local/lib/nodejs
  tar -C /usr/local/lib/nodejs --strip-components=1 -xJf /tmp/node.txz
  rm /tmp/node.txz /tmp/node.sums
  for bin in node npm npx corepack; do
    ln -sf "/usr/local/lib/nodejs/bin/${bin}" "/usr/local/bin/${bin}"
  done
fi

echo "==> golangci-lint ${GOLANGCI_VERSION}"
if ! /usr/local/bin/golangci-lint version 2>/dev/null | grep -q "${GOLANGCI_VERSION#v}"; then
  curl -fsSL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh \
    | sh -s -- -b /usr/local/bin "${GOLANGCI_VERSION}"
fi

echo "==> Docker"
if ! command -v docker >/dev/null; then
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc
  . /etc/os-release
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian ${VERSION_CODENAME} stable" \
    > /etc/apt/sources.list.d/docker.list
  apt-get update -q
  apt-get install -y -q docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
fi
if [[ -n "$DEV_USER" ]]; then
  usermod -aG docker "$DEV_USER"
fi

echo "==> Done"
go version
node --version
npm --version
golangci-lint version
docker --version
