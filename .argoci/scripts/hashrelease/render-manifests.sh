#!/bin/bash
# Renders the install manifests for the hashrelease's images into
# ${OUT}/manifests: one operator manifest and ForkliftController CR per
# platform, and the guest-conversion profiles. Fails if a rendered file is
# missing or wrong.
# The image variables below are read by name, through ${!name}.
# shellcheck disable=SC2034
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
. .argoci/scripts/hashrelease/env.sh

manifests=${OUT}/manifests
overlay=${OUT}/overlay
rm -rf "${manifests}" "${overlay}" "${overlay}-k8s"
mkdir -p "${manifests}" "${overlay}" "${overlay}-k8s"

# The ${NAME} placeholders in operator/config to fill in. Only these are
# replaced: the CRDs' description text holds other $NAMEs that must survive.
# The tunables after the images stay unset, so they render empty and the
# operator uses its built-in defaults.
OPERATOR_IMAGE=${REL_IMG}/forklift-operator:${TAG}
CONTROLLER_IMAGE=${REL_IMG}/forklift-controller:${TAG}
API_IMAGE=${REL_IMG}/forklift-api:${TAG}
VALIDATION_IMAGE=${REL_IMG}/forklift-validation:${TAG}
VIRT_V2V_IMAGE=${REL_IMG}/forklift-virt-v2v:${TAG}
VIRT_V2V_IMAGE_RHEL9=${REL_IMG}/forklift-virt-v2v-xfs:${TAG}
DEEP_INSPECTION_IMAGE=${REL_IMG}/forklift-deep-inspection:${TAG}
DEEP_INSPECTION_IMAGE_RHEL9=${REL_IMG}/forklift-deep-inspection-xfs:${TAG}
POPULATOR_CONTROLLER_IMAGE=${REL_IMG}/populator-controller:${TAG}
OVIRT_POPULATOR_IMAGE=${REL_IMG}/ovirt-populator:${TAG}
OPENSTACK_POPULATOR_IMAGE=${REL_IMG}/openstack-populator:${TAG}
VSPHERE_COPY_OFFLOAD_POPULATOR_IMAGE=${REL_IMG}/vsphere-copy-offload-populator:${TAG}
OVA_PROVIDER_SERVER_IMAGE=${REL_IMG}/forklift-ova-provider-server:${TAG}
HYPERV_PROVIDER_SERVER_IMAGE=${REL_IMG}/forklift-hyperv-provider-server:${TAG}
CLI_DOWNLOAD_IMAGE=${REL_IMG}/forklift-cli-download:${TAG}
OVA_PROXY_IMAGE=${REL_IMG}/forklift-ova-proxy:${TAG}
MUST_GATHER_IMAGE=${REL_IMG}/forklift-must-gather:${TAG}
UI_PLUGIN_IMAGE=
PLACEHOLDERS="OPERATOR_IMAGE CONTROLLER_IMAGE API_IMAGE VALIDATION_IMAGE
VIRT_V2V_IMAGE VIRT_V2V_IMAGE_RHEL9 DEEP_INSPECTION_IMAGE
DEEP_INSPECTION_IMAGE_RHEL9 POPULATOR_CONTROLLER_IMAGE OVIRT_POPULATOR_IMAGE
OPENSTACK_POPULATOR_IMAGE VSPHERE_COPY_OFFLOAD_POPULATOR_IMAGE
OVA_PROVIDER_SERVER_IMAGE HYPERV_PROVIDER_SERVER_IMAGE CLI_DOWNLOAD_IMAGE
OVA_PROXY_IMAGE MUST_GATHER_IMAGE UI_PLUGIN_IMAGE
BLOCK_OVERHEAD FILESYSTEM_OVERHEAD CDI_EXPORT_TOKEN_TTL CLEANUP_RETRIES
SNAPSHOT_REMOVAL_TIMEOUT SNAPSHOT_STATUS_CHECK_RATE OVIRT_OS_MAP
VSPHERE_OS_MAP VIRT_CUSTOMIZE_MAP VIRT_V2V_DONT_REQUEST_KVM"

# Replaces each ${NAME} in PLACEHOLDERS with that variable's value, the way
# `envsubst '<names>'` would; the build image has no envsubst.
fill_placeholders() {
  local script="" name
  for name in ${PLACEHOLDERS}; do
    script+="s|\${${name}}|${!name:-}|g;"
  done
  sed "${script}"
}

# Kustomize rejects an absolute path in resources, so an overlay reaches the
# tree by a relative one. It sets the namespace everywhere, and replaces the
# operator's WATCH_NAMESPACE, which upstream reads from a pod annotation that
# only OLM sets.
write_overlay() {  # dir
  cat > "$1/kustomization.yaml" <<EOF
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: ${NS}
resources:
- $(realpath --relative-to="$1" operator/config/default)
patches:
- target:
    kind: Deployment
    name: forklift-operator
  patch: |-
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: forklift-operator
    spec:
      template:
        spec:
          containers:
          - name: forklift-operator
            env:
            - name: WATCH_NAMESPACE
              valueFrom: null
              value: ${NS}
EOF
}
write_overlay "${overlay}"

# Plain Kubernetes has no console to serve the plugin, so its overlay also drops
# the plugin image. The lines land in the same env list, which ends the file.
write_overlay "${overlay}-k8s"
cat >> "${overlay}-k8s/kustomization.yaml" <<'EOF'
            - name: UI_PLUGIN_IMAGE
              $patch: delete
EOF

for era in ocp421 ocp422; do
  UI_PLUGIN_IMAGE=${REL_IMG}/forklift-console-plugin:${TAG}-${era}
  kubectl kustomize --load-restrictor LoadRestrictionsNone "${overlay}" \
    | fill_placeholders > "${manifests}/forklift-operator-${era}.yaml"
done
UI_PLUGIN_IMAGE=
kubectl kustomize --load-restrictor LoadRestrictionsNone "${overlay}-k8s" \
  | fill_placeholders > "${manifests}/forklift-operator-kubernetes.yaml"

# The CRs carry only per-cluster choices; every image is in the operator
# manifest. Plain Kubernetes turns off what needs OpenShift.
for era in ocp421 ocp422; do
  cat > "${manifests}/forklift-controller-${era}.yaml" <<EOF
apiVersion: forklift.konveyor.io/v1beta1
kind: ForkliftController
metadata:
  name: forklift-controller
  namespace: ${NS}
spec: {}
EOF
done
cat > "${manifests}/forklift-controller-kubernetes.yaml" <<EOF
apiVersion: forklift.konveyor.io/v1beta1
kind: ForkliftController
metadata:
  name: forklift-controller
  namespace: ${NS}
spec:
  k8s_cluster: "true"
  feature_ui_plugin: "false"
  feature_cli_download: "false"
  feature_ocp_live_migration: "false"
EOF

# Guest conversion needs the seccomp and AppArmor profiles on every node. The
# installer expects their ConfigMap to exist, so both ship in one file.
{
  kubectl create configmap forklift-virt-v2v-profiles --namespace "${NS}" \
    --from-file=hack/seccomp/unshare.json \
    --from-file=hack/seccomp/apparmor-unshare.profile \
    --dry-run=client -o yaml
  echo '---'
  cat hack/seccomp/profile-installer.yaml
} > "${manifests}/forklift-virt-v2v-profiles.yaml"

# Each check below looks for something wrong and passes when it finds nothing,
# so a missing or empty file would pass them all. Check the files first.
failed=0
fail() { echo "FAIL $*"; failed=1; }
for era in ocp421 ocp422 kubernetes; do
  grep -q '^  name: forklift-operator$' "${manifests}/forklift-operator-${era}.yaml" \
    || fail "forklift-operator-${era}.yaml has no forklift-operator resources"
done
# A correct render has no ${NAME} left at all, so any placeholder missing from
# PLACEHOLDERS shows up here too.
while read -r name; do
  fail "${name} was left unfilled"
done < <(grep -ohE '\$\{[A-Za-z_0-9]+\}' "${manifests}"/forklift-operator-*.yaml | sort -u)
grep -q 'DATASTORE_NAME' "${manifests}/forklift-operator-ocp421.yaml" \
  || fail "the CRD description text lost its \$DATASTORE_NAME"
if grep -q 'olm.targetNamespaces' "${manifests}"/forklift-operator-*.yaml; then
  fail "the WATCH_NAMESPACE patch did not apply"
fi
if grep -q 'UI_PLUGIN_IMAGE' "${manifests}/forklift-operator-kubernetes.yaml"; then
  fail "the Kubernetes manifest still sets UI_PLUGIN_IMAGE"
fi
if ! diff <(grep -v console-plugin "${manifests}/forklift-operator-ocp421.yaml") \
          <(grep -v console-plugin "${manifests}/forklift-operator-ocp422.yaml") >/dev/null; then
  fail "the two OpenShift manifests differ by more than the plugin image"
fi
grep -q calico "${manifests}/forklift-operator-ocp421.yaml" \
  || fail "the manifests have no Calico CRD fields or RBAC"

ls -l "${manifests}"
exit "${failed}"
