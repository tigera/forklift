# Settings every publish script shares. Sourced, not run.
#
# Everything is derived from the commit being built, so each step of the
# workflow arrives at the same tag and image names without passing them along.

# The commit Argo checked out. Run by hand, the tree's own HEAD.
GIT_SHA=${CI_GIT_SHA:-$(git rev-parse HEAD)}

# The branch the commit was merged to. Run by hand, the tree's own branch.
GIT_BRANCH=${CI_GIT_BRANCH:-$(git rev-parse --abbrev-ref HEAD)}

# The tag names the release the branch leads up to: rel/v3.24.0-3 builds
# v3.24.0-3-dev.g<commit>. Other branches have no release to name, so they
# build v0.0.0-dev.g<commit>. The console plugin uses the version as its
# manifest version, which the console requires to be semver, so a rel/ branch
# whose name isn't a version fails here.
if [ -z "${TAG:-}" ]; then
  case ${GIT_BRANCH} in
    rel/*)
      TAG=${GIT_BRANCH#rel/}
      [[ ${TAG} =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] \
        || { echo "branch ${GIT_BRANCH} is not named rel/v<semver>" >&2; return 1; }
      ;;
    *) TAG=v0.0.0 ;;
  esac
  TAG=${TAG}-dev.g${GIT_SHA:0:12}
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
# oras's login, kept out of $HOME and out of manifests/, so the step can delete
# it once the push is done.
ORAS_REGISTRY_CONFIG=${OUT}/registry-auth.json

# The install manifests render-manifests.sh writes. They're published as one
# OCI artifact under the images' tag, which Calico Enterprise hashreleases pull
# by tag; consumers rely on these names, so change them deliberately. The
# artifact goes to the private registry the hashreleases already read, not to
# REGISTRY with the images, which clusters must pull without credentials.
MANIFEST_FILES=(
  forklift-operator-ocp421.yaml
  forklift-operator-ocp422.yaml
  forklift-operator-kubernetes.yaml
  forklift-controller-ocp421.yaml
  forklift-controller-ocp422.yaml
  forklift-controller-kubernetes.yaml
  forklift-virt-v2v-profiles.yaml
)
MANIFESTS_REPO=${MANIFESTS_REPO:-gcr.io/unique-caldron-775/cnx/tigera/forklift-manifests}
MANIFESTS_REF=${MANIFESTS_REPO}:${TAG}
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
  echo "${REL_IMG}/forklift-console-plugin:${TAG}-ocp421"
  echo "${REL_IMG}/forklift-console-plugin:${TAG}-ocp422"
}
