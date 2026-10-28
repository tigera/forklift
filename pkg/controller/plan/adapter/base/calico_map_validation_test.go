package base

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"

	k8snet "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	api "github.com/kubev2v/forklift/pkg/apis/forklift/v1beta1"
	"github.com/kubev2v/forklift/pkg/apis/forklift/v1beta1/ref"
	calicoclient "github.com/kubev2v/forklift/pkg/lib/client/calico"
	"github.com/kubev2v/forklift/pkg/lib/logging"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// Destination-cluster fixtures for the validator tests. The Calico
// resources are unstructured, matching how the calico client reads them.

func calicoObject(gvk schema.GroupVersionKind, name string, spec map[string]interface{}) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	u.SetName(name)
	_ = unstructured.SetNestedField(u.Object, spec, "spec")
	return u
}

func felixConfig(spec map[string]interface{}) *unstructured.Unstructured {
	return calicoObject(calicoclient.FelixConfigurationGVK, "default", spec)
}

func bgpPeer(name, network string) *unstructured.Unstructured {
	return calicoObject(calicoclient.BGPPeerGVK, name, map[string]interface{}{"network": network})
}

// ipPool builds an IPPool; with no uses, spec.allowedUses is left absent so
// Calico's default (Workload, Tunnel) applies.
func ipPool(name, cidr string, disabled bool, uses ...interface{}) *unstructured.Unstructured {
	spec := map[string]interface{}{"cidr": cidr, "disabled": disabled}
	if len(uses) > 0 {
		spec["allowedUses"] = uses
	}
	return calicoObject(calicoclient.IPPoolGVK, name, spec)
}

func vlan(id int64, cidrs ...string) interface{} {
	subnets := make([]interface{}, len(cidrs))
	for i, c := range cidrs {
		subnets[i] = map[string]interface{}{"cidr": c}
	}
	return map[string]interface{}{"vlan": map[string]interface{}{"id": id}, "subnets": subnets}
}

func l2Network(name string, vlans ...interface{}) *unstructured.Unstructured {
	return calicoObject(calicoclient.NetworkGVK, name, map[string]interface{}{
		"l2Bridge": map[string]interface{}{"vlans": vlans},
	})
}

// hostEntry builds a vrf.hostConfig entry; an empty selector means the
// entry applies to every node.
func hostEntry(selector string, table int64, hasInterfaces bool) interface{} {
	e := map[string]interface{}{"routeTableIndex": table}
	if selector != "" {
		e["nodeSelector"] = selector
	}
	if hasInterfaces {
		e["hostInterfaces"] = []interface{}{"eth1"}
	}
	return e
}

func vrfNetwork(name string, entries ...interface{}) *unstructured.Unstructured {
	return calicoObject(calicoclient.NetworkGVK, name, map[string]interface{}{
		"vrf": map[string]interface{}{"hostConfig": entries},
	})
}

func testNAD(name, config string) *k8snet.NetworkAttachmentDefinition {
	return &k8snet.NetworkAttachmentDefinition{
		ObjectMeta: meta.ObjectMeta{Namespace: "ns", Name: name},
		Spec:       k8snet.NetworkAttachmentDefinitionSpec{Config: config},
	}
}

func multusPair(nad string) api.NetworkPair {
	return api.NetworkPair{Destination: api.DestinationNetwork{Type: Multus, Namespace: "ns", Name: nad}}
}

func testNADKey(name string) k8stypes.NamespacedName {
	return k8stypes.NamespacedName{Namespace: "ns", Name: name}
}

var errInjected = errors.New("injected API failure")

func kindAbsent(gvk schema.GroupVersionKind) error {
	return &apimeta.NoKindMatchError{GroupKind: gvk.GroupKind()}
}

// newDestinationClient returns a fake destination cluster holding objs.
// failures maps a Kind (for Get) or list Kind (for List) to the error the
// API server returns for it.
func newDestinationClient(t *testing.T, failures map[string]error, objs ...runtime.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := k8snet.AddToScheme(scheme); err != nil {
		t.Fatalf("scheme setup: %v", err)
	}
	for _, gvk := range []schema.GroupVersionKind{
		calicoclient.NetworkGVK,
		calicoclient.IPPoolGVK,
		calicoclient.FelixConfigurationGVK,
		calicoclient.BGPPeerGVK,
	} {
		scheme.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
		scheme.AddKnownTypeWithName(gvk.GroupVersion().WithKind(gvk.Kind+"List"), &unstructured.UnstructuredList{})
	}
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(objs...).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if err := failures[obj.GetObjectKind().GroupVersionKind().Kind]; err != nil {
					return err
				}
				return c.Get(ctx, key, obj, opts...)
			},
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if err := failures[list.GetObjectKind().GroupVersionKind().Kind]; err != nil {
					return err
				}
				return c.List(ctx, list, opts...)
			},
		}).
		Build()
}

// sortedNADIssues orders issues for comparison: findings against other VRF
// Networks follow the destination's list order, which is not part of the
// contract.
func sortedNADIssues(issues []CalicoNADIssue) []CalicoNADIssue {
	out := append([]CalicoNADIssue(nil), issues...)
	sort.Slice(out, func(i, j int) bool { return fmt.Sprint(out[i]) < fmt.Sprint(out[j]) })
	return out
}

func TestValidateCalicoNADs(t *testing.T) {
	const (
		l2Config     = `{"type":"calico","network":"net-a","vlan":100,"ipam":{"type":"calico-ipam"}}`
		vrfConfig    = `{"type":"calico","network":"vrf-a","ipam":{"type":"calico-ipam"}}`
		pinnedConfig = `{"type":"calico","network":"vrf-a","ipam":{"type":"calico-ipam","ipv4_pools":["vrf-pool"]}}`
	)
	bpfOn := felixConfig(map[string]interface{}{"bpfEnabled": true})
	nftablesOn := felixConfig(map[string]interface{}{"nftablesMode": "Enabled"})
	netA := l2Network("net-a", vlan(100, "10.100.0.0/24"))
	l2Pool := ipPool("l2-pool", "10.100.0.0/26", false, "L2Workload")
	l2PoolParsed := calicoclient.IPPool{Name: "l2-pool", CIDR: "10.100.0.0/26", AllowedUses: []string{"L2Workload"}}

	issue := func(nad string, kind CalicoIssueKind, network string, vlan uint16) CalicoNADIssue {
		return CalicoNADIssue{NAD: testNADKey(nad), Kind: kind, Network: network, VLAN: vlan}
	}
	routeIssue := func(kind CalicoIssueKind, table int64, conflictsWith string) CalicoNADIssue {
		i := issue("nad-a", kind, "vrf-a", 7)
		i.RouteTable = table
		i.ConflictsWith = conflictsWith
		return i
	}

	tests := []struct {
		name         string
		objs         []runtime.Object
		failures     map[string]error
		pairs        []api.NetworkPair
		wantIssues   []CalicoNADIssue
		wantWarnings []CalicoNADIssue
		wantCache    map[k8stypes.NamespacedName]*ResolvedCalicoNAD
	}{
		{
			name:      "NoPairs",
			wantCache: map[k8stypes.NamespacedName]*ResolvedCalicoNAD{},
		},
		{
			name: "UnreadableNADReportedOnceAndPodEntriesSkipped",
			pairs: []api.NetworkPair{
				{Destination: api.DestinationNetwork{Type: Pod}},
				multusPair("missing"),
				multusPair("missing"),
			},
			wantIssues: []CalicoNADIssue{issue("missing", CalicoIssueNADUnreadable, "", 0)},
			wantCache:  map[k8stypes.NamespacedName]*ResolvedCalicoNAD{},
		},
		{
			name: "NonCalicoNADIgnored",
			objs: []runtime.Object{
				testNAD("nad-a", `{"type":"ovn-k8s-cni-overlay","topology":"layer2"}`),
			},
			pairs:     []api.NetworkPair{multusPair("nad-a")},
			wantCache: map[k8stypes.NamespacedName]*ResolvedCalicoNAD{},
		},
		{
			name:         "CalicoNADWithoutNetworkWarns",
			objs:         []runtime.Object{testNAD("nad-a", `{"type":"calico"}`)},
			pairs:        []api.NetworkPair{multusPair("nad-a")},
			wantWarnings: []CalicoNADIssue{issue("nad-a", CalicoIssueNADMissingNetwork, "", 0)},
			wantCache:    map[k8stypes.NamespacedName]*ResolvedCalicoNAD{},
		},
		{
			name:       "NetworkNotFound",
			objs:       []runtime.Object{testNAD("nad-a", l2Config)},
			pairs:      []api.NetworkPair{multusPair("nad-a")},
			wantIssues: []CalicoNADIssue{issue("nad-a", CalicoIssueNetworkNotFound, "net-a", 100)},
			wantCache:  map[k8stypes.NamespacedName]*ResolvedCalicoNAD{},
		},
		{
			name:       "NetworkKindAbsent",
			objs:       []runtime.Object{testNAD("nad-a", l2Config)},
			failures:   map[string]error{"Network": kindAbsent(calicoclient.NetworkGVK)},
			pairs:      []api.NetworkPair{multusPair("nad-a")},
			wantIssues: []CalicoNADIssue{issue("nad-a", CalicoIssueNetworkCRDAbsent, "net-a", 100)},
			wantCache:  map[k8stypes.NamespacedName]*ResolvedCalicoNAD{},
		},
		{
			name: "NetworkWithoutL2Bridge",
			objs: []runtime.Object{
				testNAD("nad-a", l2Config),
				calicoObject(calicoclient.NetworkGVK, "net-a", map[string]interface{}{}),
			},
			pairs:      []api.NetworkPair{multusPair("nad-a")},
			wantIssues: []CalicoNADIssue{issue("nad-a", CalicoIssueNetworkHasNoL2Bridge, "net-a", 100)},
			wantCache:  map[k8stypes.NamespacedName]*ResolvedCalicoNAD{},
		},
		{
			name: "L2HealthyIsCachedWithItsEligiblePools",
			objs: []runtime.Object{
				testNAD("nad-a", l2Config), netA, bpfOn, l2Pool,
				// Neither is eligible: one is disabled, the other lacks L2Workload.
				ipPool("disabled-pool", "10.100.0.64/26", true, "L2Workload"),
				ipPool("l3-pool", "10.100.0.128/26", false),
			},
			pairs: []api.NetworkPair{multusPair("nad-a")},
			wantCache: map[k8stypes.NamespacedName]*ResolvedCalicoNAD{
				testNADKey("nad-a"): {
					Network:       "net-a",
					VLAN:          calicoclient.VLANEntry{VID: 100, Subnets: []string{"10.100.0.0/24"}},
					EligiblePools: []calicoclient.IPPool{l2PoolParsed},
				},
			},
		},
		{
			// The dataplane check runs once, against the first l2Bridge NAD.
			name: "L2Problems",
			objs: []runtime.Object{
				testNAD("nad-novlan", `{"type":"calico","network":"net-a"}`),
				testNAD("nad-wrongvlan", `{"type":"calico","network":"net-a","vlan":200}`),
				testNAD("nad-novlans", `{"type":"calico","network":"net-empty","vlan":100}`),
				testNAD("nad-nopool", l2Config),
				netA,
				calicoObject(calicoclient.NetworkGVK, "net-empty", map[string]interface{}{"l2Bridge": map[string]interface{}{}}),
			},
			pairs: []api.NetworkPair{
				multusPair("nad-novlan"),
				multusPair("nad-wrongvlan"),
				multusPair("nad-novlans"),
				multusPair("nad-nopool"),
			},
			wantIssues: []CalicoNADIssue{
				issue("nad-novlan", CalicoIssueDataplaneNotBPF, "net-a", 0),
				issue("nad-novlan", CalicoIssueVLANRequired, "net-a", 0),
				issue("nad-wrongvlan", CalicoIssueVLANNotInNetwork, "net-a", 200),
				issue("nad-novlans", CalicoIssueNetworkHasNoVLANs, "net-empty", 100),
				issue("nad-nopool", CalicoIssueVLANHasNoIPPool, "net-a", 100),
			},
			wantCache: map[k8stypes.NamespacedName]*ResolvedCalicoNAD{},
		},
		{
			name:       "IPPoolKindAbsentMeansNoPools",
			objs:       []runtime.Object{testNAD("nad-a", l2Config), netA, bpfOn},
			failures:   map[string]error{"IPPoolList": kindAbsent(calicoclient.IPPoolGVK)},
			pairs:      []api.NetworkPair{multusPair("nad-a")},
			wantIssues: []CalicoNADIssue{issue("nad-a", CalicoIssueVLANHasNoIPPool, "net-a", 100)},
			wantCache:  map[k8stypes.NamespacedName]*ResolvedCalicoNAD{},
		},
		{
			name: "VRFHealthyIsCachedWithL3Pools",
			objs: []runtime.Object{
				testNAD("nad-a", pinnedConfig),
				vrfNetwork("vrf-a", hostEntry("", 100, true)),
				vrfNetwork("vrf-b", hostEntry("", 200, true)),
				nftablesOn,
				bgpPeer("peer-a", "vrf-a"),
				ipPool("vrf-pool", "10.50.0.0/24", false),
				ipPool("disabled-pool", "10.51.0.0/24", true),
			},
			pairs: []api.NetworkPair{multusPair("nad-a")},
			wantCache: map[k8stypes.NamespacedName]*ResolvedCalicoNAD{
				testNADKey("nad-a"): {
					Network:           "vrf-a",
					IsVRF:             true,
					EligiblePools:     []calicoclient.IPPool{{Name: "vrf-pool", CIDR: "10.50.0.0/24"}},
					VRFCoversAllNodes: true,
					VRFPoolsPinned:    true,
				},
			},
		},
		{
			// Network-wide VRF checks run once per Network, so the second
			// NAD on vrf-a adds nothing. VRF NADs are cached even with
			// issues: the plan-scoped checks read them from the cache.
			name: "VRFProblems",
			objs: []runtime.Object{
				testNAD("nad-a", `{"type":"calico","network":"vrf-a","vlan":7}`),
				testNAD("nad-b", vrfConfig),
				vrfNetwork("vrf-a",
					hostEntry("rack == 'a'", 254, false),
					hostEntry("rack == 'b'", 101, true)),
				// Claims 101 on every node: a certain overlap.
				vrfNetwork("vrf-b", hostEntry("", 101, true)),
				// Claims 254 on a node subset: a possible overlap.
				vrfNetwork("vrf-c", hostEntry("rack == 'c'", 254, true)),
				// Not a VRF, so its tables are never compared.
				l2Network("l2-net", vlan(100, "10.100.0.0/24")),
				felixConfig(map[string]interface{}{
					"bpfEnabled":       true,
					"routeTableRanges": []interface{}{map[string]interface{}{"min": int64(1), "max": int64(250)}},
				}),
			},
			pairs: []api.NetworkPair{multusPair("nad-a"), multusPair("nad-b")},
			wantIssues: []CalicoNADIssue{
				routeIssue(CalicoIssueVRFRouteTableReserved, 254, ""),
				routeIssue(CalicoIssueVRFRouteTableConflict, 101, ""),
				routeIssue(CalicoIssueVRFRouteTableConflict, 101, "vrf-b"),
				issue("nad-a", CalicoIssueVRFDataplaneNotNftables, "vrf-a", 7),
			},
			wantWarnings: []CalicoNADIssue{
				issue("nad-a", CalicoIssueVRFVlanIgnored, "vrf-a", 7),
				issue("nad-a", CalicoIssueVRFNoHostInterfaces, "vrf-a", 7),
				issue("nad-a", CalicoIssueVRFNoBGPPeer, "vrf-a", 7),
				routeIssue(CalicoIssueVRFRouteTablePossibleConflict, 254, "vrf-c"),
			},
			wantCache: map[k8stypes.NamespacedName]*ResolvedCalicoNAD{
				testNADKey("nad-a"): {Network: "vrf-a", IsVRF: true, EligiblePools: []calicoclient.IPPool{}},
				testNADKey("nad-b"): {Network: "vrf-a", IsVRF: true, EligiblePools: []calicoclient.IPPool{}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newDestinationClient(t, tt.failures, tt.objs...)
			got, err := ValidateCalicoNADs(context.Background(), c, tt.pairs, logging.WithName("test"))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(sortedNADIssues(got.Issues), sortedNADIssues(tt.wantIssues)) {
				t.Errorf("issues = %+v\nwant %+v", got.Issues, tt.wantIssues)
			}
			if !reflect.DeepEqual(sortedNADIssues(got.Warnings), sortedNADIssues(tt.wantWarnings)) {
				t.Errorf("warnings = %+v\nwant %+v", got.Warnings, tt.wantWarnings)
			}
			if !reflect.DeepEqual(got.Cache.NADs, tt.wantCache) {
				t.Errorf("cache = %+v\nwant %+v", got.Cache.NADs, tt.wantCache)
			}
		})
	}
}

func TestValidateCalicoNADs_APIErrors(t *testing.T) {
	l2 := []runtime.Object{
		testNAD("nad-a", `{"type":"calico","network":"net-a","vlan":100}`),
		l2Network("net-a", vlan(100, "10.100.0.0/24")),
	}
	vrf := []runtime.Object{
		testNAD("nad-a", `{"type":"calico","network":"vrf-a"}`),
		vrfNetwork("vrf-a", hostEntry("", 100, true)),
	}
	tests := []struct {
		name    string
		objs    []runtime.Object
		failing string
	}{
		{"NetworkGet", l2, "Network"},
		{"FelixGetForL2", l2, "FelixConfiguration"},
		{"IPPoolList", l2, "IPPoolList"},
		{"BGPPeerList", vrf, "BGPPeerList"},
		{"NetworkList", vrf, "NetworkList"},
		{"FelixGetForVRF", vrf, "FelixConfiguration"},
		{"IPPoolListForVRF", vrf, "IPPoolList"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newDestinationClient(t, map[string]error{tt.failing: errInjected}, tt.objs...)
			_, err := ValidateCalicoNADs(context.Background(), c, []api.NetworkPair{multusPair("nad-a")}, nil)
			if !errors.Is(err, errInjected) {
				t.Errorf("err = %v, want the injected failure", err)
			}
		})
	}
}

func TestCalicoNADPlanIssues(t *testing.T) {
	cache := func(nads map[string]*ResolvedCalicoNAD) *CalicoValidationCache {
		c := &CalicoValidationCache{NADs: map[k8stypes.NamespacedName]*ResolvedCalicoNAD{}}
		for name, r := range nads {
			c.NADs[testNADKey(name)] = r
		}
		return c
	}
	issue := func(nad string, kind CalicoIssueKind, network string) CalicoNADIssue {
		return CalicoNADIssue{NAD: testNADKey(nad), Kind: kind, Network: network}
	}
	tests := []struct {
		name              string
		cache             *CalicoValidationCache
		preserveStaticIPs bool
		planHasPlacement  bool
		wantCriticals     []CalicoNADIssue
		wantWarnings      []CalicoNADIssue
	}{
		{name: "NilCache"},
		{
			name: "L2NADsAndHealthyVRFsRaiseNothing",
			cache: cache(map[string]*ResolvedCalicoNAD{
				"l2":  {Network: "net-a"},
				"vrf": {Network: "vrf-a", IsVRF: true, VRFCoversAllNodes: true, VRFPoolsPinned: true},
			}),
		},
		{
			name: "UnpinnedPoolWarnsUnlessStaticIPsPreserved",
			cache: cache(map[string]*ResolvedCalicoNAD{
				"vrf": {Network: "vrf-a", IsVRF: true, VRFCoversAllNodes: true},
			}),
			wantWarnings: []CalicoNADIssue{issue("vrf", CalicoIssueVRFPoolNotPinned, "vrf-a")},
		},
		{
			name: "UnpinnedPoolWithStaticIPsPreserved",
			cache: cache(map[string]*ResolvedCalicoNAD{
				"vrf": {Network: "vrf-a", IsVRF: true, VRFCoversAllNodes: true},
			}),
			preserveStaticIPs: true,
		},
		{
			// Node coverage is a property of the Network, so it is reported
			// once, against the first NAD in name order.
			name: "NodeScopedVRFWithoutPlacementIsCritical",
			cache: cache(map[string]*ResolvedCalicoNAD{
				"nad-b": {Network: "vrf-a", IsVRF: true, VRFPoolsPinned: true},
				"nad-a": {Network: "vrf-a", IsVRF: true, VRFPoolsPinned: true},
			}),
			wantCriticals: []CalicoNADIssue{issue("nad-a", CalicoIssueVRFNodeScoped, "vrf-a")},
		},
		{
			name: "NodeScopedVRFWithPlacementWarns",
			cache: cache(map[string]*ResolvedCalicoNAD{
				"nad-a": {Network: "vrf-a", IsVRF: true, VRFPoolsPinned: true},
			}),
			planHasPlacement: true,
			wantWarnings:     []CalicoNADIssue{issue("nad-a", CalicoIssueVRFPlacementUnverified, "vrf-a")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			criticals, warnings := CalicoNADPlanIssues(tt.cache, tt.preserveStaticIPs, tt.planHasPlacement)
			if !reflect.DeepEqual(criticals, tt.wantCriticals) {
				t.Errorf("criticals = %+v, want %+v", criticals, tt.wantCriticals)
			}
			if !reflect.DeepEqual(warnings, tt.wantWarnings) {
				t.Errorf("warnings = %+v, want %+v", warnings, tt.wantWarnings)
			}
		})
	}
}

func TestValidateCalicoPrimary(t *testing.T) {
	source := ref.Ref{ID: "network-1"}
	podPair := func(network string, vlan uint16) api.NetworkPair {
		return api.NetworkPair{
			Source: api.NetworkSourceRef{Ref: source},
			Destination: api.DestinationNetwork{
				Type:   Pod,
				Calico: &api.CalicoDestination{Network: network, Vlan: vlan},
			},
		}
	}
	bpfOn := felixConfig(map[string]interface{}{"bpfEnabled": true})
	netA := l2Network("net-a", vlan(100, "10.100.0.0/24"))
	l2Pool := ipPool("l2-pool", "10.100.0.0/26", false, "L2Workload")
	l3Pool := ipPool("l3-pool", "10.0.0.0/16", false)

	issue := func(kind CalicoIssueKind, network string, vlan uint16) CalicoPrimaryIssue {
		return CalicoPrimaryIssue{Kind: kind, Network: network, VLAN: vlan}
	}

	tests := []struct {
		name        string
		objs        []runtime.Object
		failures    map[string]error
		pairs       []api.NetworkPair
		wantIssues  []CalicoPrimaryIssue
		wantPrimary *ResolvedCalicoPrimary
	}{
		{name: "NoPairs"},
		{
			name:  "NoCalicoEntry",
			pairs: []api.NetworkPair{multusPair("nad-a"), {Destination: api.DestinationNetwork{Type: Pod}}},
		},
		{
			// The first pod entry is still validated so the rest of the
			// map is checked too.
			name: "MisplacedFields",
			objs: []runtime.Object{l3Pool},
			pairs: []api.NetworkPair{
				{Destination: api.DestinationNetwork{
					Type: Multus, Namespace: "ns", Name: "nad-a",
					Calico: &api.CalicoDestination{Network: "net-a", Vlan: 5},
				}},
				podPair("", 9),
				podPair("", 0),
			},
			wantIssues: []CalicoPrimaryIssue{
				issue(CalicoIssuePrimaryFieldsMisplaced, "net-a", 5),
				issue(CalicoIssuePrimaryFieldsMisplaced, "", 9),
				issue(CalicoIssuePrimaryFieldsMisplaced, "", 0),
			},
			wantPrimary: &ResolvedCalicoPrimary{
				Source:          source,
				L3EligiblePools: []calicoclient.IPPool{{Name: "l3-pool", CIDR: "10.0.0.0/16"}},
			},
		},
		{
			name:       "CalicoNotInstalled",
			failures:   map[string]error{"IPPoolList": kindAbsent(calicoclient.IPPoolGVK)},
			pairs:      []api.NetworkPair{podPair("net-a", 100)},
			wantIssues: []CalicoPrimaryIssue{issue(CalicoIssuePrimaryUnsupported, "net-a", 100)},
		},
		{
			name: "L3PrimaryIsCachedWithL3Pools",
			objs: []runtime.Object{l3Pool, l2Pool},
			// l2-pool is Workload-ineligible: its allowedUses is L2Workload only.
			pairs: []api.NetworkPair{podPair("", 0)},
			wantPrimary: &ResolvedCalicoPrimary{
				Source:          source,
				L3EligiblePools: []calicoclient.IPPool{{Name: "l3-pool", CIDR: "10.0.0.0/16"}},
			},
		},
		{
			name:       "NetworkKindAbsent",
			failures:   map[string]error{"Network": kindAbsent(calicoclient.NetworkGVK)},
			pairs:      []api.NetworkPair{podPair("net-a", 100)},
			wantIssues: []CalicoPrimaryIssue{issue(CalicoIssuePrimaryNetworkCRDAbsent, "net-a", 100)},
		},
		{
			name:       "NetworkNotFound",
			pairs:      []api.NetworkPair{podPair("net-a", 100)},
			wantIssues: []CalicoPrimaryIssue{issue(CalicoIssuePrimaryNetworkNotFound, "net-a", 100)},
		},
		{
			name:       "VRFNetworkCannotBackThePrimaryNIC",
			objs:       []runtime.Object{vrfNetwork("net-a", hostEntry("", 100, true))},
			pairs:      []api.NetworkPair{podPair("net-a", 100)},
			wantIssues: []CalicoPrimaryIssue{issue(CalicoIssuePrimaryNetworkTypeUnsupported, "net-a", 100)},
		},
		{
			name:       "NetworkWithoutL2Bridge",
			objs:       []runtime.Object{calicoObject(calicoclient.NetworkGVK, "net-a", map[string]interface{}{})},
			pairs:      []api.NetworkPair{podPair("net-a", 100)},
			wantIssues: []CalicoPrimaryIssue{issue(CalicoIssuePrimaryNetworkHasNoL2Bridge, "net-a", 100)},
		},
		{
			name:  "NotBPFAndNoVLAN",
			objs:  []runtime.Object{netA},
			pairs: []api.NetworkPair{podPair("net-a", 0)},
			wantIssues: []CalicoPrimaryIssue{
				issue(CalicoIssuePrimaryDataplaneNotBPF, "net-a", 0),
				issue(CalicoIssuePrimaryVLANRequired, "net-a", 0),
			},
		},
		{
			name:       "VLANNotInNetwork",
			objs:       []runtime.Object{netA, bpfOn},
			pairs:      []api.NetworkPair{podPair("net-a", 200)},
			wantIssues: []CalicoPrimaryIssue{issue(CalicoIssuePrimaryVLANNotInNetwork, "net-a", 200)},
		},
		{
			name: "NetworkHasNoVLANs",
			objs: []runtime.Object{
				calicoObject(calicoclient.NetworkGVK, "net-a", map[string]interface{}{"l2Bridge": map[string]interface{}{}}),
				bpfOn,
			},
			pairs:      []api.NetworkPair{podPair("net-a", 100)},
			wantIssues: []CalicoPrimaryIssue{issue(CalicoIssuePrimaryNetworkHasNoVLANs, "net-a", 100)},
		},
		{
			name:       "NoEligibleIPPool",
			objs:       []runtime.Object{netA, bpfOn, l3Pool},
			pairs:      []api.NetworkPair{podPair("net-a", 100)},
			wantIssues: []CalicoPrimaryIssue{issue(CalicoIssuePrimaryNoEligibleIPPool, "net-a", 100)},
		},
		{
			name:  "L2PrimaryIsCachedWithL2Pools",
			objs:  []runtime.Object{netA, bpfOn, l2Pool, l3Pool},
			pairs: []api.NetworkPair{podPair("net-a", 100)},
			wantPrimary: &ResolvedCalicoPrimary{
				Network:         "net-a",
				VLAN:            calicoclient.VLANEntry{VID: 100, Subnets: []string{"10.100.0.0/24"}},
				L2EligiblePools: []calicoclient.IPPool{{Name: "l2-pool", CIDR: "10.100.0.0/26", AllowedUses: []string{"L2Workload"}}},
				Source:          source,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newDestinationClient(t, tt.failures, tt.objs...)
			got, err := ValidateCalicoPrimary(context.Background(), c, tt.pairs, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got.Issues, tt.wantIssues) {
				t.Errorf("issues = %+v, want %+v", got.Issues, tt.wantIssues)
			}
			if got.Warnings != nil {
				t.Errorf("warnings = %+v, want none: plan-scoped warnings are added by the Plan controller", got.Warnings)
			}
			if !reflect.DeepEqual(got.Cache.Primary, tt.wantPrimary) {
				t.Errorf("primary = %+v, want %+v", got.Cache.Primary, tt.wantPrimary)
			}
		})
	}
}

func TestValidateCalicoPrimary_APIErrors(t *testing.T) {
	pairs := []api.NetworkPair{{Destination: api.DestinationNetwork{
		Type:   Pod,
		Calico: &api.CalicoDestination{Network: "net-a", Vlan: 100},
	}}}
	objs := []runtime.Object{l2Network("net-a", vlan(100, "10.100.0.0/24"))}
	for _, failing := range []string{"IPPoolList", "Network", "FelixConfiguration"} {
		t.Run(failing, func(t *testing.T) {
			c := newDestinationClient(t, map[string]error{failing: errInjected}, objs...)
			_, err := ValidateCalicoPrimary(context.Background(), c, pairs, logging.WithName("test"))
			if !errors.Is(err, errInjected) {
				t.Errorf("err = %v, want the injected failure", err)
			}
		})
	}
}

func TestHasCalicoPodEntry(t *testing.T) {
	calico := &api.CalicoDestination{}
	tests := []struct {
		name  string
		pairs []api.NetworkPair
		want  bool
	}{
		{"Empty", nil, false},
		{"PodWithoutCalico", []api.NetworkPair{{Destination: api.DestinationNetwork{Type: Pod}}}, false},
		{"CalicoOnMultus", []api.NetworkPair{{Destination: api.DestinationNetwork{Type: Multus, Calico: calico}}}, false},
		{"PodWithCalico", []api.NetworkPair{
			multusPair("nad-a"),
			{Destination: api.DestinationNetwork{Type: Pod, Calico: calico}},
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasCalicoPodEntry(tt.pairs); got != tt.want {
				t.Errorf("HasCalicoPodEntry = %v, want %v", got, tt.want)
			}
		})
	}
}
