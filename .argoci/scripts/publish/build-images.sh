#!/bin/bash
# Builds every image a release ships, tagged for this commit: the 16 built from
# this repo, must-gather, and one console plugin per console era, the last
# three from the fork commits forks.env pins.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
. .argoci/scripts/publish/env.sh

mkdir -p "${OUT}"

# Checks out one commit of a fork into a fresh directory, refusing a commit
# that isn't on the branch forks.env names.
checkout() {  # repo branch sha dir
  rm -rf "$4"
  git init -q "$4"
  git -C "$4" remote add origin "$1"
  git -C "$4" fetch -q --filter=blob:none origin "refs/heads/$2"
  git -C "$4" merge-base --is-ancestor "$3" FETCH_HEAD \
    || { echo "$3 is not on $2 in $1" >&2; return 1; }
  git -C "$4" checkout -q "$3"
}

echo "Building the images from this repo at ${GIT_SHA}"
targets=$(echo "${TREE_IMAGES}" | awk 'NF {print $1}')
# PLATFORM_SUFFIX= keeps the architecture out of the tags. GIT_COMMIT is left to
# the Makefile, which stamps the tree's HEAD, so check-images.sh comparing the
# label with the commit Argo asked for is a real check.
# shellcheck disable=SC2086
make ${targets} REGISTRY="${REGISTRY}" REGISTRY_ORG="${REGISTRY_ORG}" REGISTRY_TAG="${TAG}" \
  PLATFORM_SUFFIX=

# Neither fork's build sets the revision label, so it is set here, from the
# commit actually checked out, for the checks to compare against.
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)

echo "Building must-gather from ${MUST_GATHER_BRANCH} at ${MUST_GATHER_SHA}"
checkout "${MUST_GATHER_REPO}" "${MUST_GATHER_BRANCH}" "${MUST_GATHER_SHA}" "${OUT}/must-gather"
docker build --pull --platform linux/amd64 \
  --build-arg BUILD_DATE="${BUILD_DATE}" \
  --label org.opencontainers.image.revision="$(git -C "${OUT}/must-gather" rev-parse HEAD)" \
  -f "${OUT}/must-gather/build/Dockerfile" \
  -t "${REL_IMG}/forklift-must-gather:${TAG}" "${OUT}/must-gather"

for era in ocp421 ocp422; do
  branch_var=CONSOLE_PLUGIN_${era^^}_BRANCH sha_var=CONSOLE_PLUGIN_${era^^}_SHA
  echo "Building the ${era} console plugin from ${!branch_var} at ${!sha_var}"
  checkout "${CONSOLE_PLUGIN_REPO}" "${!branch_var}" "${!sha_var}" "${OUT}/console-plugin-${era}"
  docker build --pull --platform linux/amd64 \
    --build-arg VERSION="${VERSION}" --build-arg BUILD_DATE="${BUILD_DATE}" \
    --label org.opencontainers.image.revision="$(git -C "${OUT}/console-plugin-${era}" rev-parse HEAD)" \
    -f "${OUT}/console-plugin-${era}/build/Containerfile" \
    -t "${REL_IMG}/forklift-console-plugin:${TAG}-${era}" "${OUT}/console-plugin-${era}"
done
