#!/bin/bash
# Pushes the rendered manifests to ${MANIFESTS_REF} as one OCI artifact, under
# the same tag as the images they name and under ${MANIFESTS_COMMIT_TAG}, so a
# consumer can fetch them from a stable place. Expects the login to have written
# ${ORAS_REGISTRY_CONFIG}, and deletes it on exit, pass or fail, so the
# credential is on disk only for the push.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
. .argoci/scripts/publish/env.sh
trap 'rm -f "${ORAS_REGISTRY_CONFIG}"' EXIT

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
"${ORAS}" push --registry-config "${ORAS_REGISTRY_CONFIG}" \
  --artifact-type "${MANIFESTS_ARTIFACT_TYPE}" \
  "${MANIFESTS_REF},${MANIFESTS_COMMIT_TAG}" "${MANIFEST_FILES[@]}"
