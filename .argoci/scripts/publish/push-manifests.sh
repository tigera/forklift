#!/bin/bash
# Pushes the rendered manifests to ${MANIFESTS_REF} as one OCI artifact, under
# the same tag as the images they name, so a consumer can fetch them by tag
# from a stable place. Expects oras to be logged in to the registry already;
# this script never handles a credential.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
. .argoci/scripts/publish/env.sh

cd "${OUT}/manifests"
for f in "${MANIFEST_FILES[@]}"; do
  [ -s "${f}" ] || { echo "missing or empty manifest ${f}" >&2; exit 1; }
done
# Exactly the expected set, so a new or renamed manifest is a deliberate change
# to MANIFEST_FILES rather than something consumers find by surprise.
extra=$(comm -13 <(printf '%s\n' "${MANIFEST_FILES[@]}" | sort) <(find . -maxdepth 1 -type f -printf '%f\n' | sort))
[ -z "${extra}" ] || { echo "unexpected manifests: ${extra}" >&2; exit 1; }

# Bare names, run from inside the directory, so each layer's title is just the
# file name and a pull recreates them flat.
"${ORAS}" push --artifact-type "${MANIFESTS_ARTIFACT_TYPE}" \
  "${MANIFESTS_REF}" "${MANIFEST_FILES[@]}"
