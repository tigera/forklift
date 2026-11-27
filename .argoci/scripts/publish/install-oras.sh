#!/bin/bash
# Installs the pinned oras into ${OUT}/bin, checked against its release
# checksum. The build image doesn't carry oras.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
. .argoci/scripts/publish/env.sh

ORAS_VERSION=1.3.6
ORAS_SHA256=66fb0ad7206f3f6fa77ed5b552049d364ccf4b50b1684554f731f76302b52ca0
tarball=oras_${ORAS_VERSION}_linux_amd64.tar.gz

mkdir -p "$(dirname "${ORAS}")"
tmp=$(mktemp -d)
trap 'rm -rf "${tmp}"' EXIT
curl -fsSL --retry 3 -o "${tmp}/${tarball}" \
  "https://github.com/oras-project/oras/releases/download/v${ORAS_VERSION}/${tarball}"
echo "${ORAS_SHA256}  ${tmp}/${tarball}" | sha256sum -c -
tar -xzf "${tmp}/${tarball}" -C "$(dirname "${ORAS}")" oras
"${ORAS}" version
