package utils

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("AddCAToBundle", func() {
	var dir, system, ca, bundle string

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		system = filepath.Join(dir, "system.pem")
		ca = filepath.Join(dir, "cacert")
		bundle = filepath.Join(dir, "ca-bundle.crt")
		Expect(os.WriteFile(system, []byte("SYSTEM-CA"), 0o644)).To(Succeed())
		Expect(os.WriteFile(ca, []byte("PROVIDER-CA\n"), 0o644)).To(Succeed())
	})

	It("writes the system CAs followed by the provider CA", func() {
		Expect(os.WriteFile(bundle, []byte("SYSTEM-CA\n"), 0o644)).To(Succeed())
		added, err := AddCAToBundle(system, ca, bundle)
		Expect(err).NotTo(HaveOccurred())
		Expect(added).To(BeTrue())
		Expect(os.ReadFile(bundle)).To(Equal([]byte("SYSTEM-CA\nPROVIDER-CA\n")))
	})

	It("gives the same result when run twice", func() {
		Expect(os.WriteFile(bundle, nil, 0o644)).To(Succeed())
		_, err := AddCAToBundle(system, ca, bundle)
		Expect(err).NotTo(HaveOccurred())
		_, err = AddCAToBundle(system, ca, bundle)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.ReadFile(bundle)).To(Equal([]byte("SYSTEM-CA\nPROVIDER-CA\n")))
	})

	It("leaves images without a writable bundle alone", func() {
		added, err := AddCAToBundle(system, ca, bundle)
		Expect(err).NotTo(HaveOccurred())
		Expect(added).To(BeFalse())
		Expect(bundle).NotTo(BeAnExistingFile())
	})

	It("fails when the provider CA cannot be read", func() {
		Expect(os.WriteFile(bundle, nil, 0o644)).To(Succeed())
		_, err := AddCAToBundle(system, filepath.Join(dir, "missing"), bundle)
		Expect(err).To(MatchError(ContainSubstring("reading the provider CA")))
	})
})
