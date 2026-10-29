# Settings every hashrelease script shares. Sourced, not run.
#
# Everything is derived from the commit being built, so each step of the
# workflow arrives at the same tag and image names without passing them along.

# The commit Argo checked out. Run by hand, the tree's own HEAD.
GIT_SHA=${CI_GIT_SHA:-$(git rev-parse HEAD)}

# The console plugin uses the version as its manifest version, which the
# console requires to be semver, so the tag is one too. 0.0.0 keeps a
# hashrelease from ever sorting after a real release.
TAG=${TAG:-v0.0.0-main.g${GIT_SHA:0:12}}
VERSION=${TAG#v}

REGISTRY=${REGISTRY:-quay.io}
REGISTRY_ORG=${REGISTRY_ORG:-tigera}
REL_IMG=${REGISTRY}/${REGISTRY_ORG}

# The namespace the manifests install into.
NS=${NS:-konveyor-forklift}

# Where the scripts keep the forks' checkouts and write what they produce.
OUT=${OUT:-$(git rev-parse --show-toplevel)/_output/hashrelease}

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

# The images built from the forks. must-gather follows its main branch. The
# console plugin needs one image per console era: the newer consoles' plugin
# follows the fork's main branch, and the older consoles' plugin, which only
# exists on release branches, comes from the newest release that has one.
MUST_GATHER_REPO=https://github.com/tigera/forklift-must-gather.git
MUST_GATHER_BRANCH=${MUST_GATHER_BRANCH:-main}
CONSOLE_PLUGIN_REPO=https://github.com/tigera/forklift-console-plugin.git
CONSOLE_PLUGIN_OCP422_BRANCH=${CONSOLE_PLUGIN_OCP422_BRANCH:-main}

# Every image the hashrelease ships.
all_images() {
  echo "${TREE_IMAGES}" | awk -v r="${REL_IMG}" -v t="${TAG}" 'NF {print r "/" $2 ":" t}'
  echo "${REL_IMG}/forklift-must-gather:${TAG}"
  echo "${REL_IMG}/forklift-console-plugin:${TAG}-ocp421"
  echo "${REL_IMG}/forklift-console-plugin:${TAG}-ocp422"
}

# Records the fork commits the build used, so the checks compare against them.
SOURCES=${OUT}/sources.env
