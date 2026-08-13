package base

import "fmt"

// CalicoPrimaryIssueDetail formats a per-issue detail phrase for the
// CalicoPrimaryInvalid / CalicoPrimaryWarning Message. Per-VM
// issues carry a VMRef prefix; plan-level issues do not.
func CalicoPrimaryIssueDetail(i CalicoPrimaryIssue) string {
	prefix := ""
	if !i.VMRef.NotSet() {
		prefix = i.VMRef.String() + " "
	}
	switch i.Kind {
	case CalicoIssuePrimaryProviderUnsupported:
		return fmt.Sprintf("%s(PrimaryProviderUnsupported: feature is vSphere-only in this release)", prefix)
	case CalicoIssuePrimaryUnsupported:
		return fmt.Sprintf("%s(PrimaryUnsupported: Calico is not installed on the destination — projectcalico.org/v3 IPPool CRD absent)", prefix)
	case CalicoIssuePrimaryNetworkCRDAbsent:
		return fmt.Sprintf("%s(PrimaryNetworkCRDAbsent network=%q: destination Calico install does not ship the projectcalico.org/v3 Network CRD; remove calico.network or upgrade Calico)", prefix, i.Network)
	case CalicoIssuePrimaryConflictsWithUDN:
		return fmt.Sprintf("%s(PrimaryConflictsWithUDN: target namespace is labelled for a UDN primary network)", prefix)
	case CalicoIssuePrimaryNetworkNotFound:
		return fmt.Sprintf("%s(PrimaryNetworkNotFound network=%q)", prefix, i.Network)
	case CalicoIssuePrimaryNetworkTypeUnsupported:
		return fmt.Sprintf("%s(PrimaryNetworkTypeUnsupported network=%q: the referenced Network is not an L2 bridge network; only l2Bridge networks can back the primary NIC — a VRF network attaches via a multus NAD instead)", prefix, i.Network)
	case CalicoIssuePrimaryDataplaneNotBPF:
		return fmt.Sprintf("%s(PrimaryDataplaneNotBPF network=%q: the destination Calico install is not running the BPF dataplane; L2 networks require FelixConfiguration bpfEnabled: true)", prefix, i.Network)
	case CalicoIssuePrimaryNetworkHasNoL2Bridge:
		return fmt.Sprintf("%s(PrimaryNetworkHasNoL2Bridge network=%q)", prefix, i.Network)
	case CalicoIssuePrimaryNetworkHasNoVLANs:
		return fmt.Sprintf("%s(PrimaryNetworkHasNoVLANs network=%q)", prefix, i.Network)
	case CalicoIssuePrimaryVLANRequired:
		return fmt.Sprintf("%s(PrimaryVLANRequired network=%q: calico.network is set but calico.vlan is not; an explicit VLAN is required)", prefix, i.Network)
	case CalicoIssuePrimaryVLANNotInNetwork:
		return fmt.Sprintf("%s(PrimaryVLANNotInNetwork network=%q vlan=%d)", prefix, i.Network, i.VLAN)
	case CalicoIssuePrimaryNoEligibleIPPool:
		if i.IP != "" {
			return fmt.Sprintf("%s(PrimaryNoEligibleIPPool ip=%s network=%q vlan=%d)", prefix, i.IP, i.Network, i.VLAN)
		}
		return fmt.Sprintf("%s(PrimaryNoEligibleIPPool network=%q vlan=%d)", prefix, i.Network, i.VLAN)
	case CalicoIssuePrimaryIPNotInSubnet:
		return fmt.Sprintf("%s(PrimaryIPNotInSubnet ip=%s network=%q vlan=%d)", prefix, i.IP, i.Network, i.VLAN)
	case CalicoIssuePrimaryTooManyIPs:
		return fmt.Sprintf("%s(PrimaryTooManyIPs ips=%s: Calico static IP preservation supports at most one IPv4 per interface)", prefix, i.IP)
	case CalicoIssuePrimaryFieldsMisplaced:
		return fmt.Sprintf("%s(PrimaryFieldsMisplaced: calico block set on a non-pod entry, calico.vlan without calico.network, or multiple calico-flagged entries)", prefix)
	case CalicoIssuePrimaryStaticIPsNotPreserved:
		return fmt.Sprintf("%s(PrimaryStaticIPsNotPreserved: preserveStaticIPs is false; DHCP-configured guests will pick up the Calico-assigned IP via the veth, static-IP guests may have a divergent in-guest IP)", prefix)
	}
	return fmt.Sprintf("%s(%s)", prefix, i.Kind)
}

// CalicoNADIssueDetail formats a per-NAD detail phrase for the CalicoNetworkInvalid condition's Message: e.g.
// "default/foo (NetworkNotFound network=\"calico-vlan\")".
func CalicoNADIssueDetail(i CalicoNADIssue) string {
	switch i.Kind {
	case CalicoIssueNADUnreadable:
		return fmt.Sprintf("%s (NADUnreadable)", i.NAD.String())
	case CalicoIssueNetworkNotFound:
		return fmt.Sprintf("%s (NetworkNotFound network=%q)", i.NAD.String(), i.Network)
	case CalicoIssueNetworkCRDAbsent:
		return fmt.Sprintf("%s (NetworkCRDAbsent network=%q: destination Calico install does not ship the projectcalico.org/v3 Network CRD)", i.NAD.String(), i.Network)
	case CalicoIssueVRFVlanIgnored:
		return fmt.Sprintf("%s (VRFVlanIgnored network=%q vlan=%d: the referenced Network is a VRF (routed) network; VLANs apply only to l2Bridge networks and the vlan value is ignored)", i.NAD.String(), i.Network, i.VLAN)
	case CalicoIssueVRFNodeScoped:
		return fmt.Sprintf("%s (VRFNodeScoped network=%q: every hostConfig entry carries a nodeSelector, so the network exists only on matching nodes, and the plan does not constrain VM placement; VMs may schedule onto uncovered nodes and fail to start — set the plan's targetNodeSelector or targetAffinity to keep VMs on covered nodes, or add a hostConfig entry without a nodeSelector)", i.NAD.String(), i.Network)
	case CalicoIssueVRFPlacementUnverified:
		return fmt.Sprintf("%s (VRFPlacementUnverified network=%q: the network exists only on nodes matching its hostConfig selectors; the plan constrains VM placement, but Forklift cannot verify that placement keeps VMs on covered nodes)", i.NAD.String(), i.Network)
	case CalicoIssueVRFRouteTableReserved:
		return fmt.Sprintf("%s (VRFRouteTableReserved network=%q table=%d: route tables 253, 254 and 255 are reserved by the kernel; choose a different routeTableIndex)", i.NAD.String(), i.Network, i.RouteTable)
	case CalicoIssueVRFRouteTableConflict:
		if i.ConflictsWith != "" {
			return fmt.Sprintf("%s (VRFRouteTableConflict network=%q table=%d: route table %d is also claimed by VRF Network %q on an overlapping set of nodes, which can result in network outages; give each VRF Network a unique routeTableIndex)", i.NAD.String(), i.Network, i.RouteTable, i.RouteTable, i.ConflictsWith)
		}
		return fmt.Sprintf("%s (VRFRouteTableConflict network=%q table=%d: route table %d falls inside the FelixConfiguration routeTableRanges, which Calico reserves for its own routes; choose a routeTableIndex outside those ranges)", i.NAD.String(), i.Network, i.RouteTable, i.RouteTable)
	case CalicoIssueVRFRouteTablePossibleConflict:
		return fmt.Sprintf("%s (VRFRouteTablePossibleConflict network=%q table=%d: VRF Network %q also uses route table %d; both entries are node-scoped, so the overlap cannot be ruled out — verify the two selectors never match the same node, or give each network a unique routeTableIndex)", i.NAD.String(), i.Network, i.RouteTable, i.ConflictsWith, i.RouteTable)
	case CalicoIssueVRFDataplaneNotNftables:
		return fmt.Sprintf("%s (VRFDataplaneNotNftables: VRF networking requires the nftables dataplane; set nftablesMode: Enabled (and leave bpfEnabled off) in the default FelixConfiguration)", i.NAD.String())
	case CalicoIssueVRFPoolNotPinned:
		return fmt.Sprintf("%s (VRFPoolNotPinned network=%q: the NAD does not pin an IPPool, so each VM's address will come from whichever pool Calico's IPAM selects and the VRF's network may not be able to route it; pin the VRF's IPPool via ipv4_pools in the NAD's IPAM config)", i.NAD.String(), i.Network)
	case CalicoIssueVRFNoBGPPeer:
		return fmt.Sprintf("%s (VRFNoBGPPeer network=%q: cross-node reachability in this VRF requires a BGPPeer whose spec.network names it; VMs placed on different nodes will not reach each other until one exists)", i.NAD.String(), i.Network)
	case CalicoIssueVRFNoHostInterfaces:
		return fmt.Sprintf("%s (VRFNoHostInterfaces network=%q: a hostConfig entry names no hostInterfaces, so VMs on the nodes that entry matches are unreachable beyond their own node; name at least one host interface in every hostConfig entry)", i.NAD.String(), i.Network)
	case CalicoIssueDataplaneNotBPF:
		return fmt.Sprintf("%s (DataplaneNotBPF: the destination Calico install is not running the BPF dataplane; L2 networks require FelixConfiguration bpfEnabled: true)", i.NAD.String())
	case CalicoIssueNetworkHasNoL2Bridge:
		return fmt.Sprintf("%s (NetworkHasNoL2Bridge network=%q)", i.NAD.String(), i.Network)
	case CalicoIssueNetworkHasNoVLANs:
		return fmt.Sprintf("%s (NetworkHasNoVLANs network=%q)", i.NAD.String(), i.Network)
	case CalicoIssueVLANNotInNetwork:
		return fmt.Sprintf("%s (VLANNotInNetwork network=%q vlan=%d)", i.NAD.String(), i.Network, i.VLAN)
	case CalicoIssueVLANRequired:
		return fmt.Sprintf("%s (VLANRequired network=%q: the NAD references a Calico Network but names no VLAN; an explicit VLAN is required)", i.NAD.String(), i.Network)
	case CalicoIssueVLANHasNoIPPool:
		return fmt.Sprintf("%s (VLANHasNoIPPool network=%q vlan=%d)", i.NAD.String(), i.Network, i.VLAN)
	case CalicoIssueNADMissingNetwork:
		return fmt.Sprintf("%s (NADMissingNetwork: type=calico without 'network' field; MAC/IP preservation not applied)", i.NAD.String())
	}
	return fmt.Sprintf("%s (%s)", i.NAD.String(), i.Kind)
}
