package network

import (
	"context"

	api "github.com/kubev2v/forklift/pkg/apis/forklift/v1beta1"
	planbase "github.com/kubev2v/forklift/pkg/controller/plan/adapter/base"
	ocp "github.com/kubev2v/forklift/pkg/lib/client/openshift"
	liberr "github.com/kubev2v/forklift/pkg/lib/error"
	core "k8s.io/api/core/v1"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Condition types for the Calico destination checks. Same vocabulary as the
// plan-level conditions: map-scoped issues surface here, plan-scoped issues
// (placement, IP preservation, UDN conflict, per-VM) surface on the Plan.
const (
	CalicoNetworkInvalid = "CalicoNetworkInvalid"
	CalicoNetworkWarning = "CalicoNetworkWarning"
	CalicoPrimaryInvalid = "CalicoPrimaryInvalid"
	CalicoPrimaryWarning = "CalicoPrimaryWarning"
)

// validateCalico is the Calico leg of validateDestination: it validates the
// map's Calico destinations against the destination cluster — the (at most
// one) calico-flagged pod entry and every multus entry whose NAD references
// a Calico Network. Issues become NetworkMap conditions; Critical ones block
// Ready, which in turn blocks plans referencing the map. The two flags come
// from validateDestination's walk over the entries.
func (r *Reconciler) validateCalico(mp *api.NetworkMap, hasCalicoBlock, hasMultus bool) error {
	src := mp.Referenced.Provider.Source
	dst := mp.Referenced.Provider.Destination
	if src == nil || dst == nil {
		return nil
	}
	pairs := mp.Spec.Map

	// The calico block is honoured only for vSphere sources in this
	// release. Reject the explicit opt-in on other providers; NADs are
	// not probed for them (no behaviour existed to preserve).
	if src.Type() != api.VSphere {
		if hasCalicoBlock {
			if cond, ok := planbase.BuildCalicoPrimaryCondition(
				CalicoPrimaryInvalid, Critical,
				"The Calico primary-network mapping is invalid",
				[]planbase.CalicoPrimaryIssue{{Kind: planbase.CalicoIssuePrimaryProviderUnsupported}},
			); ok {
				mp.Status.SetCondition(cond)
			}
		}
		return nil
	}

	// Probe the destination only when an entry can engage Calico.
	if !hasCalicoBlock && !hasMultus {
		return nil
	}

	dstClient, err := r.destinationClient(dst)
	if err != nil {
		return liberr.Wrap(err)
	}

	nadResult, err := planbase.ValidateCalicoNADs(context.TODO(), dstClient, pairs, r.Log)
	if err != nil {
		return liberr.Wrap(err)
	}
	primaryResult, err := planbase.ValidateCalicoPrimary(context.TODO(), dstClient, pairs, r.Log)
	if err != nil {
		return liberr.Wrap(err)
	}

	if cond, ok := planbase.BuildCalicoNADCondition(
		CalicoNetworkInvalid, Critical,
		"One or more Calico Network destinations are invalid",
		nadResult.Issues,
	); ok {
		mp.Status.SetCondition(cond)
	}
	if cond, ok := planbase.BuildCalicoNADCondition(
		CalicoNetworkWarning, Warn,
		"One or more Calico NADs will not receive identity preservation",
		nadResult.Warnings,
	); ok {
		mp.Status.SetCondition(cond)
	}
	if cond, ok := planbase.BuildCalicoPrimaryCondition(
		CalicoPrimaryInvalid, Critical,
		"The Calico primary-network mapping is invalid",
		primaryResult.Issues,
	); ok {
		mp.Status.SetCondition(cond)
	}
	if cond, ok := planbase.BuildCalicoPrimaryCondition(
		CalicoPrimaryWarning, Warn,
		"The Calico primary-network mapping has informational warnings",
		primaryResult.Warnings,
	); ok {
		mp.Status.SetCondition(cond)
	}
	return nil
}

// destinationClient builds a client for the destination cluster from the
// destination provider: its secret-referenced credentials for a remote
// cluster, or the in-cluster configuration for the host provider.
func (r *Reconciler) destinationClient(provider *api.Provider) (k8sclient.Client, error) {
	if provider.IsHost() {
		return ocp.Client(provider, nil)
	}
	ref := provider.Spec.Secret
	secret := &core.Secret{}
	err := r.Get(
		context.TODO(),
		k8sclient.ObjectKey{
			Namespace: ref.Namespace,
			Name:      ref.Name,
		},
		secret)
	if err != nil {
		return nil, liberr.Wrap(err)
	}
	return ocp.Client(provider, secret)
}
