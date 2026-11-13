#!/bin/bash
# Pushes every image build-images.sh produced. Expects docker to be logged in to
# the registry already; this script never handles a credential.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
. .argoci/scripts/publish/env.sh

for image in $(all_images); do
  docker push "${image}"
done
