#!/bin/bash
# Builds every image a release ships, tagged for this hashrelease: the 16 built
# from this repo, must-gather, and one console plugin per console era.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
. .argoci/scripts/hashrelease/env.sh

mkdir -p "${OUT}"

# The commit at the tip of a branch of a remote repository.
branch_head() {  # repo branch
  local sha
  sha=$(git ls-remote --heads "$1" "refs/heads/$2" | cut -f1)
  [ -n "${sha}" ] || { echo "no branch $2 in $1" >&2; return 1; }
  echo "${sha}"
}

# The newer-console plugin's branch is fixed; the older consoles' comes from the
# newest release branch that carries one.
CONSOLE_PLUGIN_OCP421_BRANCH=${CONSOLE_PLUGIN_OCP421_BRANCH:-$(
  git ls-remote --heads "${CONSOLE_PLUGIN_REPO}" 'refs/heads/rel/*-ocp421' \
    | sed 's|.*refs/heads/||' | sort -V | tail -1)}
[ -n "${CONSOLE_PLUGIN_OCP421_BRANCH}" ] || { echo "no rel/*-ocp421 branch in ${CONSOLE_PLUGIN_REPO}" >&2; exit 1; }

# Pin each fork to the commit its branch is at now, so a push to the fork during
# the build can't mix two commits into one hashrelease. Plain assignments, so a
# failed lookup stops the script.
MUST_GATHER_SHA=$(branch_head "${MUST_GATHER_REPO}" "${MUST_GATHER_BRANCH}")
CONSOLE_PLUGIN_OCP421_SHA=$(branch_head "${CONSOLE_PLUGIN_REPO}" "${CONSOLE_PLUGIN_OCP421_BRANCH}")
CONSOLE_PLUGIN_OCP422_SHA=$(branch_head "${CONSOLE_PLUGIN_REPO}" "${CONSOLE_PLUGIN_OCP422_BRANCH}")
cat > "${SOURCES}" <<EOF
MUST_GATHER_BRANCH=${MUST_GATHER_BRANCH}
MUST_GATHER_SHA=${MUST_GATHER_SHA}
CONSOLE_PLUGIN_OCP421_BRANCH=${CONSOLE_PLUGIN_OCP421_BRANCH}
CONSOLE_PLUGIN_OCP421_SHA=${CONSOLE_PLUGIN_OCP421_SHA}
CONSOLE_PLUGIN_OCP422_BRANCH=${CONSOLE_PLUGIN_OCP422_BRANCH}
CONSOLE_PLUGIN_OCP422_SHA=${CONSOLE_PLUGIN_OCP422_SHA}
EOF
cat "${SOURCES}"

# Checks out one commit of a fork into a fresh directory.
checkout() {  # repo sha dir
  rm -rf "$3"
  git init -q "$3"
  git -C "$3" fetch -q --depth 1 "$1" "$2"
  git -C "$3" checkout -q FETCH_HEAD
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
checkout "${MUST_GATHER_REPO}" "${MUST_GATHER_SHA}" "${OUT}/must-gather"
docker build --pull --platform linux/amd64 \
  --build-arg BUILD_DATE="${BUILD_DATE}" \
  --label org.opencontainers.image.revision="$(git -C "${OUT}/must-gather" rev-parse HEAD)" \
  -f "${OUT}/must-gather/build/Dockerfile" \
  -t "${REL_IMG}/forklift-must-gather:${TAG}" "${OUT}/must-gather"

for era in ocp421 ocp422; do
  branch_var=CONSOLE_PLUGIN_${era^^}_BRANCH sha_var=CONSOLE_PLUGIN_${era^^}_SHA
  echo "Building the ${era} console plugin from ${!branch_var} at ${!sha_var}"
  checkout "${CONSOLE_PLUGIN_REPO}" "${!sha_var}" "${OUT}/console-plugin-${era}"
  docker build --pull --platform linux/amd64 \
    --build-arg VERSION="${VERSION}" --build-arg BUILD_DATE="${BUILD_DATE}" \
    --label org.opencontainers.image.revision="$(git -C "${OUT}/console-plugin-${era}" rev-parse HEAD)" \
    -f "${OUT}/console-plugin-${era}/build/Containerfile" \
    -t "${REL_IMG}/forklift-console-plugin:${TAG}-${era}" "${OUT}/console-plugin-${era}"
done
