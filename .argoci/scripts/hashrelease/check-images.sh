#!/bin/bash
# Checks each image build-images.sh produced came from the commit it should
# have: this repo's images from the commit being built, and the forks' images
# from the commits build-images.sh recorded. Also checks each console plugin
# loads on the consoles it is built for, and that no image has package updates
# left to install. Fails if any check does.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
. .argoci/scripts/hashrelease/env.sh
# shellcheck source=/dev/null # written by build-images.sh
. "${SOURCES}"

failed=0

check_revision() {  # image expected-commit
  local got
  got=$(docker image inspect "$1" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}') || got="(image missing)"
  if [ "${got}" = "$2" ]; then
    echo "OK   $1"
  else
    echo "FAIL $1 was built from ${got:-(no revision label)}, want $2"
    failed=1
  fi
}

# The Makefile stamps the tree's HEAD, so this also catches a checkout that
# isn't the commit Argo was asked to build.
for image in $(echo "${TREE_IMAGES}" | awk 'NF {print $2}'); do
  check_revision "${REL_IMG}/${image}:${TAG}" "${GIT_SHA}"
done
check_revision "${REL_IMG}/forklift-must-gather:${TAG}" "${MUST_GATHER_SHA}"
check_revision "${REL_IMG}/forklift-console-plugin:${TAG}-ocp421" "${CONSOLE_PLUGIN_OCP421_SHA}"
check_revision "${REL_IMG}/forklift-console-plugin:${TAG}-ocp422" "${CONSOLE_PLUGIN_OCP422_SHA}"

# The console loader changed at 4.22, so each era's plugin must call the entry
# function its consoles expect. Consoles up to 4.21 also reject any plugin ID
# but <name>@<manifest version>.
for era in ocp421 ocp422; do
  image=${REL_IMG}/forklift-console-plugin:${TAG}-${era}
  dir=${OUT}/plugin-files-${era}
  rm -rf "${dir}"
  container=$(docker create "${image}" true)
  docker cp "${container}:/opt/app-root/src" "${dir}"
  docker rm "${container}" >/dev/null
  case ${era} in
    ocp421) want="loadPluginEntry(\"forklift-console-plugin@${VERSION}\"" ;;
    ocp422) want='__load_plugin_entry__("forklift-console-plugin"' ;;
  esac
  if ! grep -qE "\"version\": *\"${VERSION}\"" "${dir}/plugin-manifest.json"; then
    echo "FAIL ${image} has manifest version $(grep -oE '"version": *"[^"]*"' "${dir}/plugin-manifest.json"), want ${VERSION}"
    failed=1
  elif ! grep -qF "${want}" "${dir}"/plugin-entry.*.min.js; then
    echo "FAIL ${image} does not call ${want}"
    failed=1
  else
    echo "OK   ${image} loads with ${want}"
  fi
done

# Each Containerfile updates its packages, but a layer served from the build
# cache looks the same as one that ran, so ask each image's package manager
# whether anything is left. PENDING means the update didn't run; ERROR means
# the question couldn't be asked, which proves nothing either way. The script
# runs in each image's shell, so it expands there, not here.
# shellcheck disable=SC2016
pending_updates='if command -v dnf >/dev/null 2>&1; then dnf -q check-update >/dev/null 2>&1; rc=$?; else microdnf -y --assumeno update >/tmp/u 2>&1; rc=$?; grep -qE "^ *Upgrading|Upgrading:" /tmp/u && rc=100; fi
case $rc in 0) echo CLEAN;; 100) echo PENDING;; *) echo "ERROR rc=$rc";; esac'
for image in $(all_images); do
  result=$(docker run --rm --user 0 --entrypoint sh "${image}" -c "${pending_updates}")
  if [ "${result}" = CLEAN ]; then
    echo "OK   ${image} has no package updates left"
  else
    echo "FAIL ${image} package update check: ${result}"
    failed=1
  fi
done

exit "${failed}"
