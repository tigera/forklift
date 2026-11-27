# Settings every publish script shares. Sourced, not run.
#
# Everything is derived from the commit being built, except TAG: a dev tag
# pushed mid-publish would change a second describe, so the workflow describes
# once and hands TAG to later steps.

# The commit Argo checked out. Run by hand, the tree's own HEAD.
GIT_SHA=${CI_GIT_SHA:-$(git rev-parse HEAD)}

# The tag is the commit's git describe against the v<semver> dev tags on its
# branch, with exactly these flags, so anyone who describes the same commit the
# same way names this build. Describe needs the tags and the history back to
# them, which a CI checkout may lack. The console plugin uses the
# version as its manifest version, which the console requires to be semver, so
# a commit with no dev tag behind it fails here rather than describing as a
# bare commit.
describe_tags=(--tags --match 'v[0-9]*.[0-9]*.[0-9]*')
if [ -z "${TAG:-}" ]; then
  # Always, since a stale clone can still describe, against an older tag.
  if [ "$(git rev-parse --is-shallow-repository)" = true ]; then
    fetch_args=(--unshallow --tags --force)
  else
    fetch_args=(--tags --force)
  fi
  git fetch -q "${fetch_args[@]}" origin \
    || { echo "fetching tags failed; not describing ${GIT_SHA} against stale ones" >&2; return 1; }
  TAG=$(git describe "${describe_tags[@]}" --always --long --abbrev=12 "${GIT_SHA}")
  [[ ${TAG} =~ ^v[0-9]+\.[0-9]+\.[0-9]+-[0-9A-Za-z.-]+$ ]] \
    || { echo "${GIT_SHA} describes as ${TAG}; push a v<semver> dev tag behind it" >&2; return 1; }
fi
VERSION=${TAG#v}

REGISTRY=${REGISTRY:-quay.io}
REGISTRY_ORG=${REGISTRY_ORG:-tigeradev}
REL_IMG=${REGISTRY}/${REGISTRY_ORG}

# The namespace the manifests install into.
NS=${NS:-konveyor-forklift}

# Where the scripts keep the forks' checkouts and write what they produce.
OUT=${OUT:-$(git rev-parse --show-toplevel)/_output/publish}

ORAS=${OUT}/bin/oras
# oras's login: outside the workspace, so nothing that uploads or builds from
# it can pick the credential up, and out of $HOME, so it can be deleted.
ORAS_REGISTRY_CONFIG=/tmp/forklift-registry-auth.json

# Each version also needs a pinned console plugin commit and a loader check;
# the build fails without either.
OCP_VERSIONS=(4.21 4.22)
OCP_ERAS=()
for version in "${OCP_VERSIONS[@]}"; do
  OCP_ERAS+=("ocp${version//./}")
done

# The install manifests render-manifests.sh writes, published as one OCI
# artifact under the images' tag. Consumers fetch them by these names, so
# change them deliberately. The artifact goes to MANIFESTS_REPO, not to
# REGISTRY with the images, which clusters must pull without credentials.
MANIFEST_FILES=(forklift-virt-v2v-profiles.yaml)
for era in "${OCP_ERAS[@]}" kubernetes; do
  MANIFEST_FILES+=("forklift-operator-${era}.yaml" "forklift-controller-${era}.yaml")
done
# Tagging dev tags onto a commit already published changes its describe, and
# so TAG, without a new publish; this second tag on the artifact names the
# commit's latest publish whatever its describe.
MANIFESTS_COMMIT_TAG=g${GIT_SHA:0:12}
MANIFESTS_ARTIFACT_TYPE=application/vnd.tigera.forklift.manifests

# The images built from this repo, as "<make target> <image name>". Each one's
# Containerfile stamps org.opencontainers.image.revision with the commit.
TREE_IMAGES="
build-api-image forklift-api
build-controller-image forklift-controller
build-validation-image forklift-validation
build-operator-image forklift-operator
build-virt-v2v-image forklift-virt-v2v
build-virt-v2v-xfs-image forklift-virt-v2v-xfs
build-deep-inspection-image forklift-deep-inspection
build-deep-inspection-xfs-image forklift-deep-inspection-xfs
build-populator-controller-image populator-controller
build-ovirt-populator-image ovirt-populator
build-openstack-populator-image openstack-populator
build-vsphere-copy-offload-populator-image vsphere-copy-offload-populator
build-ova-provider-server-image forklift-ova-provider-server
build-hyperv-provider-server-image forklift-hyperv-provider-server
build-cli-download-image forklift-cli-download
build-ova-proxy-image forklift-ova-proxy
"

# The images built from the forks, each at the commit forks.env pins, so what
# gets published was reviewed in this repo.
MUST_GATHER_REPO=https://github.com/tigera/forklift-must-gather.git
CONSOLE_PLUGIN_REPO=https://github.com/tigera/forklift-console-plugin.git
# shellcheck source=/dev/null
. "$(git rev-parse --show-toplevel)/.argoci/publish/forks.env"

# Every image a publish ships.
all_images() {
  echo "${TREE_IMAGES}" | awk -v r="${REL_IMG}" -v t="${TAG}" 'NF {print r "/" $2 ":" t}'
  echo "${REL_IMG}/forklift-must-gather:${TAG}"
  for era in "${OCP_ERAS[@]}"; do
    echo "${REL_IMG}/forklift-console-plugin:${TAG}-${era}"
  done
}
