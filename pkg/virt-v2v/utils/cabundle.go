package utils

import (
	"bytes"
	"errors"
	"fmt"
	"os"
)

// AddCAToBundle rewrites bundle as the CAs in system followed by the CA in ca.
//
// virt-v2v reads vSphere disks with curl and nbdkit's curl plugin, which trust only
// the system CA bundle and offer no way to pass another one. The image points the
// system bundle at a file the pod can write, and this adds the provider's CA to it.
// Images built without that file are left alone.
func AddCAToBundle(system, ca, bundle string) (added bool, err error) {
	if _, err = os.Stat(bundle); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	systemCAs, err := os.ReadFile(system)
	if err != nil {
		return false, fmt.Errorf("reading the system CA bundle: %w", err)
	}
	providerCA, err := os.ReadFile(ca)
	if err != nil {
		return false, fmt.Errorf("reading the provider CA: %w", err)
	}
	var out bytes.Buffer
	out.Write(systemCAs)
	if len(systemCAs) > 0 && !bytes.HasSuffix(systemCAs, []byte("\n")) {
		out.WriteByte('\n')
	}
	out.Write(providerCA)
	if len(providerCA) > 0 && !bytes.HasSuffix(providerCA, []byte("\n")) {
		out.WriteByte('\n')
	}
	if err = os.WriteFile(bundle, out.Bytes(), 0o644); err != nil {
		return false, fmt.Errorf("writing the CA bundle: %w", err)
	}
	return true, nil
}
