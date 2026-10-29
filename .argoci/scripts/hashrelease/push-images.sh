#!/bin/bash
# Pushes the hashrelease's images.
#
# Not set up yet: the registry and its credentials are still to be decided.
# Until they are, this only lists what it would push, so the rendered manifests
# name images that don't exist anywhere.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
. .argoci/scripts/hashrelease/env.sh

for image in $(all_images); do
  echo "not pushed: ${image}"
done
