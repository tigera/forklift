package network

import (
	api "github.com/kubev2v/forklift/pkg/apis/forklift/v1beta1"
	planbase "github.com/kubev2v/forklift/pkg/controller/plan/adapter/base"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("NetworkMap Calico validation", func() {
	makeMap := func(srcType api.ProviderType, pairs ...api.NetworkPair) *api.NetworkMap {
		src := &api.Provider{
			ObjectMeta: meta.ObjectMeta{Name: "src", Namespace: "test"},
			Spec:       api.ProviderSpec{Type: &srcType},
		}
		dstType := api.OpenShift
		dst := &api.Provider{
			ObjectMeta: meta.ObjectMeta{Name: "dst", Namespace: "test"},
			Spec:       api.ProviderSpec{Type: &dstType},
		}
		mp := &api.NetworkMap{
			ObjectMeta: meta.ObjectMeta{Name: "test-map", Namespace: "test"},
			Spec:       api.NetworkMapSpec{Map: pairs},
		}
		mp.Provider.Source = src
		mp.Provider.Destination = dst
		return mp
	}
	calicoPodPair := api.NetworkPair{
		Destination: api.DestinationNetwork{
			Type:   Pod,
			Calico: &api.CalicoDestination{},
		},
	}
	podPair := api.NetworkPair{
		Destination: api.DestinationNetwork{Type: Pod},
	}

	It("rejects the calico opt-in on a non-vSphere source", func() {
		mp := makeMap(api.OVirt, calicoPodPair)
		r := &Reconciler{}
		Expect(r.validateCalico(mp, planbase.HasCalicoPodEntry(mp.Spec.Map), false)).To(Succeed())
		cond := mp.Status.FindCondition(CalicoPrimaryInvalid)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Category).To(Equal(Critical))
		Expect(cond.Message).To(ContainSubstring("PrimaryProviderUnsupported"))
	})

	It("sets no condition on a non-vSphere source without the calico opt-in", func() {
		mp := makeMap(api.OVirt, podPair)
		r := &Reconciler{}
		Expect(r.validateCalico(mp, planbase.HasCalicoPodEntry(mp.Spec.Map), false)).To(Succeed())
		Expect(mp.Status.HasCondition(CalicoPrimaryInvalid)).To(BeFalse())
	})

	It("skips the destination probe when no entry can engage Calico", func() {
		// A vSphere-source map with neither a calico block nor a multus
		// entry returns before building the destination client — a nil
		// Reconciler client would panic otherwise.
		mp := makeMap(api.VSphere, podPair)
		r := &Reconciler{}
		Expect(r.validateCalico(mp, planbase.HasCalicoPodEntry(mp.Spec.Map), false)).To(Succeed())
		Expect(mp.Status.HasCondition(CalicoPrimaryInvalid)).To(BeFalse())
		Expect(mp.Status.HasCondition(CalicoNetworkInvalid)).To(BeFalse())
	})

	It("is a no-op when providers are not referenced yet", func() {
		mp := &api.NetworkMap{Spec: api.NetworkMapSpec{Map: []api.NetworkPair{calicoPodPair}}}
		r := &Reconciler{}
		Expect(r.validateCalico(mp, planbase.HasCalicoPodEntry(mp.Spec.Map), false)).To(Succeed())
		Expect(mp.Status.HasCondition(CalicoPrimaryInvalid)).To(BeFalse())
	})

	It("warns that networkIPMode is ignored on a Calico pod entry, whatever its value", func() {
		for _, mode := range []api.NetworkIPMode{api.NetworkIPModePreserve, api.NetworkIPModeDHCP, api.NetworkIPModeNone} {
			pair := calicoPodPair
			pair.NetworkIPMode = mode
			mp := makeMap(api.VSphere, pair)
			warnCalicoIPModeIgnored(mp)
			cond := mp.Status.FindCondition(NetworkIPModeIgnored)
			Expect(cond).NotTo(BeNil(), string(mode))
			Expect(cond.Category).To(Equal(Warn))
			Expect(cond.Message).To(ContainSubstring("ignored"))
			Expect(cond.Message).To(ContainSubstring("preserveStaticIPs"))
		}
	})

	It("does not warn about networkIPMode when it is unset or the entry is not Calico", func() {
		plain := podPair
		plain.NetworkIPMode = api.NetworkIPModeDHCP
		for _, pair := range []api.NetworkPair{calicoPodPair, plain} {
			mp := makeMap(api.VSphere, pair)
			warnCalicoIPModeIgnored(mp)
			Expect(mp.Status.HasCondition(NetworkIPModeIgnored)).To(BeFalse())
		}
	})

	It("keeps the condition vocabulary aligned with the plan controller", func() {
		// The plan controller folds per-VM issues into conditions of the
		// same names; a rename on either side would fork the user-facing
		// vocabulary.
		Expect(CalicoPrimaryInvalid).To(Equal("CalicoPrimaryInvalid"))
		Expect(CalicoNetworkInvalid).To(Equal("CalicoNetworkInvalid"))
		var _ = planbase.CalicoIssuePrimaryProviderUnsupported
	})
})
