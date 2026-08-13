package base

import (
	"context"
	"fmt"
	"sort"
	"strings"

	api "github.com/kubev2v/forklift/pkg/apis/forklift/v1beta1"
	ocpmodel "github.com/kubev2v/forklift/pkg/controller/provider/model/ocp"
	calicoclient "github.com/kubev2v/forklift/pkg/lib/client/calico"
	libcnd "github.com/kubev2v/forklift/pkg/lib/condition"
	liberr "github.com/kubev2v/forklift/pkg/lib/error"
	"github.com/kubev2v/forklift/pkg/lib/logging"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	k8stypes "k8s.io/apimachinery/pkg/types"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ValidateCalicoNADs walks every Multus destination in a NetworkMap's
// entries, fetches each Calico-referencing NAD from the destination cluster,
// and validates the NAD/Network/IPPool resources. It evaluates only
// map-scoped concerns — checks that need Plan state (placement, IP
// preservation) are evaluated separately by CalicoNADPlanIssues over the
// returned cache. Called by both the NetworkMap controller (to surface map
// conditions) and the Plan controller (to build the per-VM cache).
func ValidateCalicoNADs(ctx context.Context, c k8sclient.Client, pairs []api.NetworkPair, log logging.LevelLogger) (CalicoValidationResult, error) {
	result := CalicoValidationResult{
		Cache: &CalicoValidationCache{
			NADs: map[k8stypes.NamespacedName]*ResolvedCalicoNAD{},
		},
	}
	if len(pairs) == 0 {
		return result, nil
	}

	nadCfgs := map[k8stypes.NamespacedName]*ocpmodel.NetworkConfig{}
	var pools []calicoclient.IPPool
	poolsLoaded := false
	bpfChecked := false

	vrfChecked := map[string]bool{}
	vrfDataplaneChecked := false
	var vrfNetworks []calicoclient.Network
	vrfNetworksLoaded := false
	var bgpPeerNetworks map[string]bool
	bgpPeerNetworksLoaded := false
	var felixFacts *calicoclient.FelixConfig

	// Felix is the per-node daemon which programs the dataplane
	// based on k8s config.
	loadFelix := func(key k8stypes.NamespacedName) (*calicoclient.FelixConfig, error) {
		if felixFacts != nil {
			return felixFacts, nil
		}
		fc, err := calicoclient.GetFelixConfig(ctx, c)
		if err != nil {
			return nil, liberr.Wrap(err, "nad", key.String())
		}
		felixFacts = fc
		return fc, nil
	}

	// IPPools are the Calico CRD describing available IP CIDRs
	// and their permitted uses.
	loadPools := func(key k8stypes.NamespacedName) (err error) {
		if poolsLoaded {
			return
		}
		pools, err = calicoclient.ListIPPools(ctx, c)
		if err != nil {
			if !meta.IsNoMatchError(err) {
				return liberr.Wrap(err, "nad", key.String())
			}
			pools = nil
			err = nil
		}
		poolsLoaded = true
		return
	}

	// For each map item, try fetching a corresponding NAD.
	// Search for NAD's whose config has `type:calico`, ignore the rest.
	// The presence of a Calico `Network` CR implies per-NIC addressing
	// is possible for L2 and L3.
	for _, pair := range pairs {
		if pair.Destination.Type != Multus {
			continue
		}
		key := k8stypes.NamespacedName{
			Namespace: pair.Destination.Namespace,
			Name:      pair.Destination.Name,
		}
		if _, dup := nadCfgs[key]; dup {
			continue
		}

		cfg, err := FetchAndParseNAD(ctx, c, key.Namespace, key.Name)
		if err != nil {
			nadCfgs[key] = nil
			if log != nil {
				log.Error(err, "Calico NAD: failed to fetch/parse",
					"namespace", key.Namespace, "name", key.Name)
			}
			result.Issues = append(result.Issues, CalicoNADIssue{
				NAD:  key,
				Kind: CalicoIssueNADUnreadable,
			})
			continue
		}
		nadCfgs[key] = cfg

		// type:calico without a "network" field is Calico's legacy L3 IPAM
		// mode. Identity preservation on secondary NICs relies on the
		// network-scoped annotations that ship with the Network resource, so
		// it applies only to NADs that reference one (l2Bridge or VRF); warn
		// that MAC/IP annotations will not be emitted for NICs mapped here.
		if cfg.Type == ocpmodel.CalicoCNIType && cfg.Network == "" {
			result.Warnings = append(result.Warnings, CalicoNADIssue{
				NAD:  key,
				Kind: CalicoIssueNADMissingNetwork,
			})
			continue
		}
		if !cfg.ReferencesCalicoNetwork() {
			continue
		}

		issueBase := CalicoNADIssue{NAD: key, Network: cfg.Network, VLAN: cfg.VLAN}

		nw, err := calicoclient.GetNetwork(ctx, c, cfg.Network)
		if err != nil {
			switch {
			case meta.IsNoMatchError(err):
				issueBase.Kind = CalicoIssueNetworkCRDAbsent
				result.Issues = append(result.Issues, issueBase)
				continue
			case k8serr.IsNotFound(err):
				issueBase.Kind = CalicoIssueNetworkNotFound
				result.Issues = append(result.Issues, issueBase)
				continue
			default:
				return CalicoValidationResult{}, liberr.Wrap(err, "nad", key.String(), "network", cfg.Network)
			}
		}

		// Classify the Network before any VLAN handling (VRF / l2Bridge).
		if nw.IsVRF {
			if cfg.VLAN != 0 {
				ib := issueBase
				ib.Kind = CalicoIssueVRFVlanIgnored
				result.Warnings = append(result.Warnings, ib)
			}

			if !vrfChecked[cfg.Network] {
				vrfChecked[cfg.Network] = true

				if vrfHasEntryWithoutHostInterfaces(nw.VRFHostConfig) {
					ib := issueBase
					ib.Kind = CalicoIssueVRFNoHostInterfaces
					result.Warnings = append(result.Warnings, ib)
				}

				if !bgpPeerNetworksLoaded {
					bgpPeerNetworksLoaded = true
					bgpPeerNetworks, err = calicoclient.ListBGPPeerNetworks(ctx, c)
					if err != nil {
						return CalicoValidationResult{}, liberr.Wrap(err, "nad", key.String())
					}
				}
				if !bgpPeerNetworks[cfg.Network] {
					ib := issueBase
					ib.Kind = CalicoIssueVRFNoBGPPeer
					result.Warnings = append(result.Warnings, ib)
				}

				if !vrfNetworksLoaded {
					vrfNetworksLoaded = true
					vrfNetworks, err = calicoclient.ListNetworks(ctx, c)
					if err != nil {
						return CalicoValidationResult{}, liberr.Wrap(err, "nad", key.String())
					}
				}
				felix, ferr := loadFelix(key)
				if ferr != nil {
					return CalicoValidationResult{}, ferr
				}
				criticals, warns := vrfRouteTableIssues(issueBase, nw, vrfNetworks, felix)
				result.Issues = append(result.Issues, criticals...)
				result.Warnings = append(result.Warnings, warns...)

				// VRF networks only supported on NFTables dataplane.
				if !vrfDataplaneChecked {
					vrfDataplaneChecked = true
					if felix.BPFEnabled || felix.NftablesMode != calicoclient.NftablesModeEnabled {
						ib := issueBase
						ib.Kind = CalicoIssueVRFDataplaneNotNftables
						result.Issues = append(result.Issues, ib)
					}
				}
			}
			if err = loadPools(key); err != nil {
				return CalicoValidationResult{}, err
			}
			result.Cache.NADs[key] = &ResolvedCalicoNAD{
				Network:           cfg.Network,
				IsVRF:             true,
				EligiblePools:     calicoclient.L3EligiblePools(pools),
				VRFCoversAllNodes: vrfHasAllNodesEntry(nw.VRFHostConfig),
				VRFPoolsPinned:    len(cfg.IPv4Pools) > 0,
			}
			continue
		}
		if nw.L2Bridge == nil {
			issueBase.Kind = CalicoIssueNetworkHasNoL2Bridge
			result.Issues = append(result.Issues, issueBase)
			continue
		}

		// L2 networks require the BPF dataplane.
		if !bpfChecked {
			bpfChecked = true
			bpfEnabled, err := calicoclient.GetBPFEnabled(ctx, c)
			if err != nil {
				return CalicoValidationResult{}, liberr.Wrap(err, "nad", key.String())
			}
			if !bpfEnabled {
				ib := issueBase
				ib.Kind = CalicoIssueDataplaneNotBPF
				result.Issues = append(result.Issues, ib)
			}
		}

		if cfg.VLAN == 0 {
			issueBase.Kind = CalicoIssueVLANRequired
			result.Issues = append(result.Issues, issueBase)
			continue
		}

		entry, vlanIssueKind := resolveVLANEntry(nw.L2Bridge.VLANs, cfg.VLAN)
		if vlanIssueKind != "" {
			issueBase.Kind = vlanIssueKind
			result.Issues = append(result.Issues, issueBase)
			continue
		}

		if err = loadPools(key); err != nil {
			return CalicoValidationResult{}, err
		}
		eligible := calicoclient.L2WorkloadEligiblePools(pools, entry.Subnets)
		if len(eligible) == 0 {
			issueBase.Kind = CalicoIssueVLANHasNoIPPool
			result.Issues = append(result.Issues, issueBase)
			continue
		}

		result.Cache.NADs[key] = &ResolvedCalicoNAD{
			Network:       cfg.Network,
			VLAN:          *entry,
			EligiblePools: eligible,
		}
	}
	return result, nil
}

// CalicoNADPlanIssues evaluates the plan-scoped concerns for the cached VRF
// NADs: pool pinning (depends on the Plan's preserveStaticIPs) and node
// coverage (depends on whether the Plan constrains VM placement). Returns
// Critical issues and warnings for the plan-level Calico conditions.
func CalicoNADPlanIssues(cache *CalicoValidationCache, preserveStaticIPs, planHasPlacement bool) (criticals, warnings []CalicoNADIssue) {
	if cache == nil {
		return
	}
	keys := make([]k8stypes.NamespacedName, 0, len(cache.NADs))
	for key := range cache.NADs {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })

	coverageChecked := map[string]bool{}
	for _, key := range keys {
		resolved := cache.NADs[key]
		if !resolved.IsVRF {
			continue
		}
		issueBase := CalicoNADIssue{NAD: key, Network: resolved.Network}

		if !resolved.VRFPoolsPinned && !preserveStaticIPs {
			ib := issueBase
			ib.Kind = CalicoIssueVRFPoolNotPinned
			warnings = append(warnings, ib)
		}

		// An empty nodeSelector is the canonical all-nodes form, so
		// all-scoped hostConfig means a node subset (Calico selectors
		// are not parsed). A VM scheduled onto an uncovered node
		// fails at CNI ADD, so with no placement constraints on the
		// plan the outcome is a scheduling lottery: Critical. When
		// the plan pins placement (targetNodeSelector/targetAffinity)
		// the user has taken control; Warn that the pin cannot be
		// verified against the network's selectors.
		if !coverageChecked[resolved.Network] {
			coverageChecked[resolved.Network] = true
			if !resolved.VRFCoversAllNodes {
				ib := issueBase
				if planHasPlacement {
					ib.Kind = CalicoIssueVRFPlacementUnverified
					warnings = append(warnings, ib)
				} else {
					ib.Kind = CalicoIssueVRFNodeScoped
					criticals = append(criticals, ib)
				}
			}
		}
	}
	return
}

// ValidateCalicoPrimary validates the (at most one) calico-flagged
// NetworkMap entry — a type: pod destination carrying the calico field.
// It evaluates only map-scoped concerns — the Plan controller separately
// evaluates the preserveStaticIPs warning and the UDN-namespace conflict.
// Called by both the NetworkMap controller and the Plan controller.
func ValidateCalicoPrimary(ctx context.Context, c k8sclient.Client, pairs []api.NetworkPair, log logging.LevelLogger) (CalicoPrimaryValidationResult, error) {
	result := CalicoPrimaryValidationResult{
		Cache: &CalicoPrimaryValidationCache{},
	}
	if len(pairs) == 0 {
		return result, nil
	}

	// Classify entries (VRF or L2Bridge), surface field-misplacement issues.
	var calicoEntries []api.NetworkPair
	for _, pair := range pairs {
		dest := pair.Destination
		if dest.Calico == nil {
			continue
		}
		if dest.Type != Pod {
			result.Issues = append(result.Issues, CalicoPrimaryIssue{
				Kind:    CalicoIssuePrimaryFieldsMisplaced,
				Network: dest.Calico.Network,
				VLAN:    dest.Calico.Vlan,
			})
			continue
		}
		calicoEntries = append(calicoEntries, pair)
		// vlan-without-network is a field-placement error within the block.
		if dest.Calico.Network == "" && dest.Calico.Vlan != 0 {
			result.Issues = append(result.Issues, CalicoPrimaryIssue{
				Kind: CalicoIssuePrimaryFieldsMisplaced,
				VLAN: dest.Calico.Vlan,
			})
		}
	}

	if len(calicoEntries) > 1 {
		result.Issues = append(result.Issues, CalicoPrimaryIssue{
			Kind: CalicoIssuePrimaryFieldsMisplaced,
		})
	}
	if len(calicoEntries) == 0 {
		return result, nil
	}

	entry := calicoEntries[0]
	calico := entry.Destination.Calico
	issueBase := CalicoPrimaryIssue{Network: calico.Network, VLAN: calico.Vlan}

	pools, err := calicoclient.ListIPPools(ctx, c)
	if err != nil {
		if meta.IsNoMatchError(err) {
			ib := issueBase
			ib.Kind = CalicoIssuePrimaryUnsupported
			result.Issues = append(result.Issues, ib)
			return result, nil
		}
		return CalicoPrimaryValidationResult{}, liberr.Wrap(err, "network", calico.Network)
	}

	if calico.Network == "" {
		result.Cache.Primary = &ResolvedCalicoPrimary{
			Source:          entry.Source.Ref,
			L3EligiblePools: calicoclient.L3EligiblePools(pools),
		}
		return result, nil
	}

	nw, err := calicoclient.GetNetwork(ctx, c, calico.Network)
	if err != nil {
		switch {
		case meta.IsNoMatchError(err):
			ib := issueBase
			ib.Kind = CalicoIssuePrimaryNetworkCRDAbsent
			result.Issues = append(result.Issues, ib)
			return result, nil
		case k8serr.IsNotFound(err):
			ib := issueBase
			ib.Kind = CalicoIssuePrimaryNetworkNotFound
			result.Issues = append(result.Issues, ib)
			return result, nil
		default:
			if log != nil {
				log.Error(err, "Calico-primary: failed to fetch Network",
					"network", calico.Network)
			}
			return CalicoPrimaryValidationResult{}, liberr.Wrap(err, "network", calico.Network)
		}
	}

	// A non-l2Bridge network (e.g. a VRF network) carries no VLAN, so
	// the VLANRequired / VLAN-matching checks below would mislead.
	if nw.IsVRF {
		ib := issueBase
		ib.Kind = CalicoIssuePrimaryNetworkTypeUnsupported
		result.Issues = append(result.Issues, ib)
		return result, nil
	}
	if nw.L2Bridge == nil {
		ib := issueBase
		ib.Kind = CalicoIssuePrimaryNetworkHasNoL2Bridge
		result.Issues = append(result.Issues, ib)
		return result, nil
	}

	// L2 networks require the BPF dataplane.
	bpfEnabled, err := calicoclient.GetBPFEnabled(ctx, c)
	if err != nil {
		return CalicoPrimaryValidationResult{}, liberr.Wrap(err, "network", calico.Network)
	}
	if !bpfEnabled {
		ib := issueBase
		ib.Kind = CalicoIssuePrimaryDataplaneNotBPF
		result.Issues = append(result.Issues, ib)
	}

	// A Network reference requires an explicit VLAN.
	if calico.Vlan == 0 {
		ib := issueBase
		ib.Kind = CalicoIssuePrimaryVLANRequired
		result.Issues = append(result.Issues, ib)
		return result, nil
	}

	vlanEntry, vlanIssueKind := resolveVLANEntry(nw.L2Bridge.VLANs, calico.Vlan)
	if vlanIssueKind != "" {
		ib := issueBase
		ib.Kind = translateVLANIssueKindToPrimary(vlanIssueKind)
		result.Issues = append(result.Issues, ib)
		return result, nil
	}

	l2Pools := calicoclient.L2WorkloadEligiblePools(pools, vlanEntry.Subnets)
	if len(l2Pools) == 0 {
		ib := issueBase
		ib.Kind = CalicoIssuePrimaryNoEligibleIPPool
		result.Issues = append(result.Issues, ib)
		return result, nil
	}

	result.Cache.Primary = &ResolvedCalicoPrimary{
		Network:         calico.Network,
		VLAN:            *vlanEntry,
		L2EligiblePools: l2Pools,
		Source:          entry.Source.Ref,
	}
	return result, nil
}

// HasCalicoPodEntry reports whether any entry is a type: pod destination
// carrying the calico opt-in block.
func HasCalicoPodEntry(pairs []api.NetworkPair) bool {
	for _, pair := range pairs {
		if pair.Destination.Type == Pod && pair.Destination.Calico != nil {
			return true
		}
	}
	return false
}

// translateVLANIssueKindToPrimary converts the secondary-NAD-path VLAN issue
// kinds returned by resolveVLANEntry into the Calico-primary equivalents.
// The shared resolver returns the NAD-path kinds; the primary path emits its
// own kinds so users can disambiguate primary vs secondary failures in the
// condition.
func translateVLANIssueKindToPrimary(k CalicoIssueKind) CalicoIssueKind {
	switch k {
	case CalicoIssueNetworkHasNoVLANs:
		return CalicoIssuePrimaryNetworkHasNoVLANs
	case CalicoIssueVLANNotInNetwork:
		return CalicoIssuePrimaryVLANNotInNetwork
	}
	return k
}

// vrfReservedRouteTables are the kernel's own route tables — 253 (default),
// 254 (main), 255 (local) — which a VRF must never claim.
var vrfReservedRouteTables = map[int64]bool{253: true, 254: true, 255: true}

// vrfHasAllNodesEntry reports whether any spec.vrf.hostConfig entry has an
// empty (or absent) nodeSelector — such an entry applies to every node, so
// the VRF network is guaranteed to exist wherever a VM lands.
func vrfHasAllNodesEntry(entries []calicoclient.VRFHostEntry) bool {
	for _, e := range entries {
		if e.NodeSelector == "" {
			return true
		}
	}
	return false
}

// vrfHasEntryWithoutHostInterfaces reports whether any spec.vrf.hostConfig
// entry names no host interfaces. Such an entry gives pods on its nodes no
// path off the node inside the VRF, so its VMs are unreachable beyond their
// own node.
func vrfHasEntryWithoutHostInterfaces(entries []calicoclient.VRFHostEntry) bool {
	for _, e := range entries {
		if !e.HasHostInterfaces {
			return true
		}
	}
	return false
}

// vrfRouteTableIssues checks the referenced VRF Network's routeTableIndex
// values for reserved-table use and for collisions with other VRF Networks
// and with the FelixConfiguration routeTableRanges. issueBase supplies the
// NAD/Network attribution; each finding carries the offending table index.
//
// A collision with another VRF Network is provable (Critical) when at least
// one of the two entries carrying the index has no nodeSelector — an
// all-nodes entry overlaps any other node set. When both entries are
// selector-scoped, the overlap depends on which nodes the selectors match;
// selector evaluation is deliberately out of scope, so those pairs are
// reported as possible conflicts (Warn). Entries of the same Network sharing
// an index are legitimate — same VRF, same table — and never reported.
//
// The FelixConfiguration sub-check runs only when spec.routeTableRanges is
// explicitly set. When absent, Felix falls back to version-dependent
// defaults that are not modelled here — guessing them would risk false
// Criticals against tables Felix never touches.
func vrfRouteTableIssues(issueBase CalicoNADIssue, nw *calicoclient.Network, all []calicoclient.Network, felix *calicoclient.FelixConfig) (criticals, warnings []CalicoNADIssue) {
	// Distinct indexes in entry order; per index, remember whether any
	// entry carrying it applies to all nodes.
	var indexes []int64
	seen := map[int64]bool{}
	allNodes := map[int64]bool{}
	for _, e := range nw.VRFHostConfig {
		if !seen[e.RouteTableIndex] {
			seen[e.RouteTableIndex] = true
			indexes = append(indexes, e.RouteTableIndex)
		}
		if e.NodeSelector == "" {
			allNodes[e.RouteTableIndex] = true
		}
	}

	for _, idx := range indexes {
		if vrfReservedRouteTables[idx] {
			ib := issueBase
			ib.Kind = CalicoIssueVRFRouteTableReserved
			ib.RouteTable = idx
			criticals = append(criticals, ib)
		}
		for _, rng := range felix.RouteTableRanges {
			if idx >= rng.Min && idx <= rng.Max {
				ib := issueBase
				ib.Kind = CalicoIssueVRFRouteTableConflict
				ib.RouteTable = idx
				criticals = append(criticals, ib)
				break
			}
		}
	}

	for i := range all {
		other := &all[i]
		if other.Name == nw.Name || !other.IsVRF {
			continue
		}
		otherHas := map[int64]bool{}
		otherAllNodes := map[int64]bool{}
		for _, o := range other.VRFHostConfig {
			otherHas[o.RouteTableIndex] = true
			if o.NodeSelector == "" {
				otherAllNodes[o.RouteTableIndex] = true
			}
		}
		for _, idx := range indexes {
			if !otherHas[idx] {
				continue
			}
			ib := issueBase
			ib.RouteTable = idx
			ib.ConflictsWith = other.Name
			if allNodes[idx] || otherAllNodes[idx] {
				ib.Kind = CalicoIssueVRFRouteTableConflict
				criticals = append(criticals, ib)
			} else {
				ib.Kind = CalicoIssueVRFRouteTablePossibleConflict
				warnings = append(warnings, ib)
			}
		}
	}
	return
}

// resolveVLANEntry returns the l2Bridge.vlans[] entry matched by nadVLAN.
func resolveVLANEntry(vlans []calicoclient.VLANEntry, nadVLAN uint16) (*calicoclient.VLANEntry, CalicoIssueKind) {
	if len(vlans) == 0 {
		return nil, CalicoIssueNetworkHasNoVLANs
	}
	for i := range vlans {
		if vlans[i].VID == nadVLAN {
			return &vlans[i], ""
		}
	}
	return nil, CalicoIssueVLANNotInNetwork
}

// BuildCalicoNADCondition assembles a Calico NAD condition from a slice of
// per-NAD issues. Items are deduplicated by NAD reference; Message
// concatenates a per-issue detail phrase for each (including duplicates, so
// distinct kinds against the same NAD are both visible). Returns ok=false
// when issues is empty so callers can skip SetCondition. Used for both
// NetworkMap and Plan conditions.
func BuildCalicoNADCondition(condType, category, baseMsg string, issues []CalicoNADIssue) (libcnd.Condition, bool) {
	if len(issues) == 0 {
		return libcnd.Condition{}, false
	}
	cond := libcnd.Condition{
		Type:     condType,
		Status:   libcnd.True,
		Reason:   calicoConditionReason,
		Category: category,
		Items:    []string{},
	}
	details := make([]string, 0, len(issues))
	seen := map[string]bool{}
	for _, issue := range issues {
		ref := issue.NAD.String()
		if !seen[ref] {
			seen[ref] = true
			cond.Items = append(cond.Items, ref)
		}
		details = append(details, CalicoNADIssueDetail(issue))
	}
	cond.Message = fmt.Sprintf("%s: %s.", baseMsg, strings.Join(details, "; "))
	return cond, true
}

// BuildCalicoPrimaryCondition assembles a Calico-primary condition from a
// slice of issues. Items deduplicate by issue ref (per-VM VMRef when set, or
// a "(plan)" synthetic identifier for map/plan-level issues). Message
// concatenates a per-issue detail phrase. Returns ok=false when issues is
// empty so callers can skip SetCondition. Used for both NetworkMap and Plan
// conditions.
func BuildCalicoPrimaryCondition(condType, category, baseMsg string, issues []CalicoPrimaryIssue) (libcnd.Condition, bool) {
	if len(issues) == 0 {
		return libcnd.Condition{}, false
	}
	cond := libcnd.Condition{
		Type:     condType,
		Status:   libcnd.True,
		Reason:   calicoConditionReason,
		Category: category,
		Items:    []string{},
	}
	details := make([]string, 0, len(issues))
	seen := map[string]bool{}
	for _, issue := range issues {
		item := calicoPrimaryIssueItem(issue)
		if !seen[item] {
			seen[item] = true
			cond.Items = append(cond.Items, item)
		}
		details = append(details, CalicoPrimaryIssueDetail(issue))
	}
	cond.Message = fmt.Sprintf("%s: %s.", baseMsg, strings.Join(details, "; "))
	return cond, true
}

// calicoConditionReason is the Reason set on every Calico condition.
const calicoConditionReason = "NotValid"

// calicoPrimaryIssueItem returns the Items entry for an issue. Per-VM issues
// use the VMRef; plan-level issues use a synthetic "(plan)" identifier since
// there is no resource-specific ref to attach (the offending NetworkMap entry
// is plan-scoped).
func calicoPrimaryIssueItem(i CalicoPrimaryIssue) string {
	if !i.VMRef.NotSet() {
		return i.VMRef.String()
	}
	return "(plan)"
}
