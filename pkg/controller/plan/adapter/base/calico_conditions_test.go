package base

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kubev2v/forklift/pkg/apis/forklift/v1beta1/ref"
	libcnd "github.com/kubev2v/forklift/pkg/lib/condition"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// The detail tests pin each kind's tag and fields (the part users and
// scripts match on) and check the explanatory prose is present without
// pinning its wording.

func TestCalicoNADIssueDetail(t *testing.T) {
	nad := k8stypes.NamespacedName{Namespace: "ns", Name: "nad-a"}
	issue := func(kind CalicoIssueKind) CalicoNADIssue {
		return CalicoNADIssue{NAD: nad, Kind: kind, Network: "net-a", VLAN: 100, RouteTable: 254}
	}
	withConflict := issue(CalicoIssueVRFRouteTableConflict)
	withConflict.ConflictsWith = "net-b"
	possible := issue(CalicoIssueVRFRouteTablePossibleConflict)
	possible.ConflictsWith = "net-b"

	tests := []struct {
		name       string
		issue      CalicoNADIssue
		wantPrefix string
		wantSubstr string
	}{
		{"NADUnreadable", issue(CalicoIssueNADUnreadable), "ns/nad-a (NADUnreadable)", ""},
		{"NetworkNotFound", issue(CalicoIssueNetworkNotFound), `ns/nad-a (NetworkNotFound network="net-a")`, ""},
		{"NetworkCRDAbsent", issue(CalicoIssueNetworkCRDAbsent), `ns/nad-a (NetworkCRDAbsent network="net-a": `, "Network CRD"},
		{"VRFVlanIgnored", issue(CalicoIssueVRFVlanIgnored), `ns/nad-a (VRFVlanIgnored network="net-a" vlan=100: `, "ignored"},
		{"VRFNodeScoped", issue(CalicoIssueVRFNodeScoped), `ns/nad-a (VRFNodeScoped network="net-a": `, "targetNodeSelector"},
		{"VRFPlacementUnverified", issue(CalicoIssueVRFPlacementUnverified), `ns/nad-a (VRFPlacementUnverified network="net-a": `, "cannot verify"},
		{"VRFRouteTableReserved", issue(CalicoIssueVRFRouteTableReserved), `ns/nad-a (VRFRouteTableReserved network="net-a" table=254: `, "reserved by the kernel"},
		{"VRFRouteTableConflictWithNetwork", withConflict, `ns/nad-a (VRFRouteTableConflict network="net-a" table=254: `, `VRF Network "net-b"`},
		{"VRFRouteTableConflictWithFelix", issue(CalicoIssueVRFRouteTableConflict), `ns/nad-a (VRFRouteTableConflict network="net-a" table=254: `, "routeTableRanges"},
		{"VRFRouteTablePossibleConflict", possible, `ns/nad-a (VRFRouteTablePossibleConflict network="net-a" table=254: `, `VRF Network "net-b"`},
		{"VRFDataplaneNotNftables", issue(CalicoIssueVRFDataplaneNotNftables), "ns/nad-a (VRFDataplaneNotNftables: ", "nftablesMode: Enabled"},
		{"VRFPoolNotPinned", issue(CalicoIssueVRFPoolNotPinned), `ns/nad-a (VRFPoolNotPinned network="net-a": `, "ipv4_pools"},
		{"VRFNoBGPPeer", issue(CalicoIssueVRFNoBGPPeer), `ns/nad-a (VRFNoBGPPeer network="net-a": `, "BGPPeer"},
		{"VRFNoHostInterfaces", issue(CalicoIssueVRFNoHostInterfaces), `ns/nad-a (VRFNoHostInterfaces network="net-a": `, "hostInterfaces"},
		{"DataplaneNotBPF", issue(CalicoIssueDataplaneNotBPF), "ns/nad-a (DataplaneNotBPF: ", "bpfEnabled: true"},
		{"NetworkHasNoL2Bridge", issue(CalicoIssueNetworkHasNoL2Bridge), `ns/nad-a (NetworkHasNoL2Bridge network="net-a")`, ""},
		{"NetworkHasNoVLANs", issue(CalicoIssueNetworkHasNoVLANs), `ns/nad-a (NetworkHasNoVLANs network="net-a")`, ""},
		{"VLANNotInNetwork", issue(CalicoIssueVLANNotInNetwork), `ns/nad-a (VLANNotInNetwork network="net-a" vlan=100)`, ""},
		{"VLANRequired", issue(CalicoIssueVLANRequired), `ns/nad-a (VLANRequired network="net-a": `, "explicit VLAN"},
		{"VLANHasNoIPPool", issue(CalicoIssueVLANHasNoIPPool), `ns/nad-a (VLANHasNoIPPool network="net-a" vlan=100)`, ""},
		{"NADMissingNetwork", issue(CalicoIssueNADMissingNetwork), "ns/nad-a (NADMissingNetwork: ", "not applied"},
		// A kind with no dedicated phrase falls back to its bare name.
		{"Fallback", issue(CalicoIssueIPNotInSubnet), "ns/nad-a (IPNotInSubnet)", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalicoNADIssueDetail(tt.issue)
			if !strings.HasPrefix(got, tt.wantPrefix) {
				t.Errorf("detail = %q, want prefix %q", got, tt.wantPrefix)
			}
			if tt.wantSubstr == "" {
				if got != tt.wantPrefix {
					t.Errorf("detail = %q, want exactly %q", got, tt.wantPrefix)
				}
			} else if !strings.Contains(got, tt.wantSubstr) {
				t.Errorf("detail = %q, want it to mention %q", got, tt.wantSubstr)
			}
		})
	}
}

func TestCalicoPrimaryIssueDetail(t *testing.T) {
	issue := func(kind CalicoIssueKind) CalicoPrimaryIssue {
		return CalicoPrimaryIssue{Kind: kind, Network: "net-a", VLAN: 100, IP: "10.0.0.5"}
	}
	noIPPool := issue(CalicoIssuePrimaryNoEligibleIPPool)
	noIPPool.IP = ""

	tests := []struct {
		name       string
		issue      CalicoPrimaryIssue
		wantPrefix string
		wantSubstr string
	}{
		{"ProviderUnsupported", issue(CalicoIssuePrimaryProviderUnsupported), "(PrimaryProviderUnsupported: ", "vSphere-only"},
		{"Unsupported", issue(CalicoIssuePrimaryUnsupported), "(PrimaryUnsupported: ", "IPPool CRD absent"},
		{"NetworkCRDAbsent", issue(CalicoIssuePrimaryNetworkCRDAbsent), `(PrimaryNetworkCRDAbsent network="net-a": `, "Network CRD"},
		{"ConflictsWithUDN", issue(CalicoIssuePrimaryConflictsWithUDN), "(PrimaryConflictsWithUDN: ", "UDN"},
		{"NetworkNotFound", issue(CalicoIssuePrimaryNetworkNotFound), `(PrimaryNetworkNotFound network="net-a")`, ""},
		{"NetworkTypeUnsupported", issue(CalicoIssuePrimaryNetworkTypeUnsupported), `(PrimaryNetworkTypeUnsupported network="net-a": `, "l2Bridge"},
		{"DataplaneNotBPF", issue(CalicoIssuePrimaryDataplaneNotBPF), `(PrimaryDataplaneNotBPF network="net-a": `, "bpfEnabled: true"},
		{"NetworkHasNoL2Bridge", issue(CalicoIssuePrimaryNetworkHasNoL2Bridge), `(PrimaryNetworkHasNoL2Bridge network="net-a")`, ""},
		{"NetworkHasNoVLANs", issue(CalicoIssuePrimaryNetworkHasNoVLANs), `(PrimaryNetworkHasNoVLANs network="net-a")`, ""},
		{"VLANRequired", issue(CalicoIssuePrimaryVLANRequired), `(PrimaryVLANRequired network="net-a": `, "explicit VLAN"},
		{"VLANNotInNetwork", issue(CalicoIssuePrimaryVLANNotInNetwork), `(PrimaryVLANNotInNetwork network="net-a" vlan=100)`, ""},
		{"NoEligibleIPPoolWithIP", issue(CalicoIssuePrimaryNoEligibleIPPool), `(PrimaryNoEligibleIPPool ip=10.0.0.5 network="net-a" vlan=100)`, ""},
		{"NoEligibleIPPoolWithoutIP", noIPPool, `(PrimaryNoEligibleIPPool network="net-a" vlan=100)`, ""},
		{"IPNotInSubnet", issue(CalicoIssuePrimaryIPNotInSubnet), `(PrimaryIPNotInSubnet ip=10.0.0.5 network="net-a" vlan=100)`, ""},
		{"TooManyIPs", issue(CalicoIssuePrimaryTooManyIPs), "(PrimaryTooManyIPs ips=10.0.0.5: ", "at most one IPv4"},
		{"FieldsMisplaced", issue(CalicoIssuePrimaryFieldsMisplaced), "(PrimaryFieldsMisplaced: ", "non-pod entry"},
		{"StaticIPsNotPreserved", issue(CalicoIssuePrimaryStaticIPsNotPreserved), "(PrimaryStaticIPsNotPreserved: ", "preserveStaticIPs is false"},
		// A kind with no dedicated phrase falls back to its bare name.
		{"Fallback", issue(CalicoIssueNADUnreadable), "(NADUnreadable)", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalicoPrimaryIssueDetail(tt.issue)
			if !strings.HasPrefix(got, tt.wantPrefix) {
				t.Errorf("detail = %q, want prefix %q", got, tt.wantPrefix)
			}
			if tt.wantSubstr == "" {
				if got != tt.wantPrefix {
					t.Errorf("detail = %q, want exactly %q", got, tt.wantPrefix)
				}
			} else if !strings.Contains(got, tt.wantSubstr) {
				t.Errorf("detail = %q, want it to mention %q", got, tt.wantSubstr)
			}
		})
	}

	t.Run("PerVMIssueIsPrefixedWithVMRef", func(t *testing.T) {
		i := issue(CalicoIssuePrimaryNetworkNotFound)
		i.VMRef = ref.Ref{ID: "vm-1", Name: "web"}
		want := i.VMRef.String() + ` (PrimaryNetworkNotFound network="net-a")`
		if got := CalicoPrimaryIssueDetail(i); got != want {
			t.Errorf("detail = %q, want %q", got, want)
		}
	})
}

func TestBuildCalicoNADCondition(t *testing.T) {
	t.Run("NoIssuesBuildsNothing", func(t *testing.T) {
		if _, ok := BuildCalicoNADCondition("T", "Critical", "msg", nil); ok {
			t.Error("ok = true, want false")
		}
	})

	t.Run("ItemsDedupedButEveryIssueInMessage", func(t *testing.T) {
		a := k8stypes.NamespacedName{Namespace: "ns", Name: "nad-a"}
		b := k8stypes.NamespacedName{Namespace: "ns", Name: "nad-b"}
		issues := []CalicoNADIssue{
			{NAD: a, Kind: CalicoIssueNetworkNotFound, Network: "net-a"},
			{NAD: b, Kind: CalicoIssueNADUnreadable},
			{NAD: a, Kind: CalicoIssueVLANRequired, Network: "net-a"},
		}
		cond, ok := BuildCalicoNADCondition("CalicoNetworkInvalid", "Critical", "Calico NADs are invalid", issues)
		if !ok {
			t.Fatal("ok = false, want true")
		}
		want := libcnd.Condition{
			Type:     "CalicoNetworkInvalid",
			Status:   libcnd.True,
			Reason:   calicoConditionReason,
			Category: "Critical",
			Items:    []string{"ns/nad-a", "ns/nad-b"},
			Message: "Calico NADs are invalid: " +
				CalicoNADIssueDetail(issues[0]) + "; " +
				CalicoNADIssueDetail(issues[1]) + "; " +
				CalicoNADIssueDetail(issues[2]) + ".",
		}
		if !reflect.DeepEqual(cond, want) {
			t.Errorf("condition = %+v\nwant %+v", cond, want)
		}
	})
}

func TestBuildCalicoPrimaryCondition(t *testing.T) {
	t.Run("NoIssuesBuildsNothing", func(t *testing.T) {
		if _, ok := BuildCalicoPrimaryCondition("T", "Warn", "msg", nil); ok {
			t.Error("ok = true, want false")
		}
	})

	t.Run("PlanIssuesShareOneItemAndVMIssuesUseTheirRef", func(t *testing.T) {
		vm := ref.Ref{ID: "vm-1", Name: "web"}
		issues := []CalicoPrimaryIssue{
			{Kind: CalicoIssuePrimaryFieldsMisplaced},
			{Kind: CalicoIssuePrimaryNetworkNotFound, Network: "net-a"},
			{VMRef: vm, Kind: CalicoIssuePrimaryTooManyIPs, IP: "10.0.0.5,10.0.0.6"},
			{VMRef: vm, Kind: CalicoIssuePrimaryIPNotInSubnet, IP: "10.0.0.5"},
		}
		cond, ok := BuildCalicoPrimaryCondition("CalicoPrimaryInvalid", "Critical", "Calico primary is invalid", issues)
		if !ok {
			t.Fatal("ok = false, want true")
		}
		details := make([]string, len(issues))
		for i, issue := range issues {
			details[i] = CalicoPrimaryIssueDetail(issue)
		}
		want := libcnd.Condition{
			Type:     "CalicoPrimaryInvalid",
			Status:   libcnd.True,
			Reason:   calicoConditionReason,
			Category: "Critical",
			Items:    []string{"(plan)", vm.String()},
			Message:  "Calico primary is invalid: " + strings.Join(details, "; ") + ".",
		}
		if !reflect.DeepEqual(cond, want) {
			t.Errorf("condition = %+v\nwant %+v", cond, want)
		}
	})
}
