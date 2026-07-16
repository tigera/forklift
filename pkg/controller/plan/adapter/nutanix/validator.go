package nutanix

import (
	api "github.com/kubev2v/forklift/pkg/apis/forklift/v1beta1"
	"github.com/kubev2v/forklift/pkg/apis/forklift/v1beta1/ref"
	planbase "github.com/kubev2v/forklift/pkg/controller/plan/adapter/base"
	plancontext "github.com/kubev2v/forklift/pkg/controller/plan/context"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Validator struct {
	*plancontext.Context
}

func (r *Validator) WarmMigration() bool {
	return false
}

func (r *Validator) MigrationType() bool {
	switch r.Plan.Spec.Type {
	case api.MigrationCold, "":
		return true
	default:
		return false
	}
}

func (r *Validator) StorageMapped(_ ref.Ref) (bool, error) {
	// TODO: validate storage container mappings against VM disks
	return true, nil
}

func (r *Validator) DirectStorage(_ ref.Ref) (bool, error) {
	return true, nil
}

func (r *Validator) NetworksMapped(_ ref.Ref) (bool, error) {
	// TODO: validate subnet mappings against VM NICs
	return true, nil
}

func (r *Validator) MaintenanceMode(_ ref.Ref) (bool, error) {
	return true, nil
}

func (r *Validator) NICNetworkRefs(_ ref.Ref) ([]ref.Ref, error) {
	// TODO: return one ref per VM NIC subnet
	return nil, nil
}

func (r *Validator) StaticIPs(_ ref.Ref) (bool, error) {
	return true, nil
}

func (r *Validator) UdnStaticIPs(_ ref.Ref, _ client.Client) (bool, error) {
	return true, nil
}

func (r *Validator) SharedDisks(_ ref.Ref, _ client.Client) (bool, string, string, error) {
	return true, "", "", nil
}

func (r *Validator) ExcludedDisks(_ ref.Ref) (bool, string, string, error) {
	return true, "", "", nil
}

func (r *Validator) ChangeTrackingEnabled(_ ref.Ref) (bool, error) {
	return true, nil
}

func (r *Validator) HasSnapshot(_ ref.Ref) (bool, string, string, error) {
	return true, "", "", nil
}

func (r *Validator) PowerState(_ ref.Ref) (bool, error) {
	return true, nil
}

func (r *Validator) VMMigrationType(_ ref.Ref) (bool, error) {
	return true, nil
}

func (r *Validator) InvalidDiskSizes(_ ref.Ref) ([]string, error) {
	// TODO: return disk UUIDs with invalid sizes
	return nil, nil
}

func (r *Validator) MacConflicts(_ ref.Ref) ([]planbase.MacConflict, error) {
	// TODO: check source NIC MACs against destination inventory
	return nil, nil
}

func (r *Validator) PVCNameTemplate(_ ref.Ref, _ string) (bool, error) {
	// TODO: validate PVC name template against VM disks
	return true, nil
}

func (r *Validator) GuestToolsInstalled(_ ref.Ref) (bool, error) {
	return true, nil
}

func (r *Validator) ConsolidationNeeded(_ ref.Ref) (bool, error) {
	return false, nil
}

// ValidateCalicoNADs returns empty results. Non-vSphere providers aren't a
// target for Calico-Network IP/MAC preservation today.
func (r *Validator) ValidateCalicoNADs(_ client.Client) (planbase.CalicoValidationResult, error) {
	return planbase.CalicoValidationResult{}, nil
}

// CalicoVMIssues returns no issues. Non-vSphere providers aren't a target
// for Calico-Network IP/MAC preservation today.
func (r *Validator) CalicoVMIssues(_ ref.Ref, _ *planbase.CalicoValidationCache) ([]planbase.CalicoIssue, error) {
	return nil, nil
}

// ValidateCalicoPrimary scans the NetworkMap. If any calico-flagged entry
// exists, returns a single CalicoIssuePrimaryProviderUnsupported issue —
// the feature is not supported on this provider in this release.
func (r *Validator) ValidateCalicoPrimary(_ client.Client) (planbase.CalicoPrimaryValidationResult, error) {
	if r.Plan.Referenced.Map.Network == nil {
		return planbase.CalicoPrimaryValidationResult{}, nil
	}
	for _, pair := range r.Plan.Referenced.Map.Network.Spec.Map {
		if pair.Destination.Calico != nil {
			return planbase.CalicoPrimaryValidationResult{
				Issues: []planbase.CalicoPrimaryIssue{{Kind: planbase.CalicoIssuePrimaryProviderUnsupported}},
			}, nil
		}
	}
	return planbase.CalicoPrimaryValidationResult{}, nil
}

// CalicoPrimaryIssues returns nil; any calico-flagged entry was already
// rejected at plan level by ValidateCalicoPrimary.
func (r *Validator) CalicoPrimaryIssues(_ ref.Ref, _ *planbase.CalicoPrimaryValidationCache) ([]planbase.CalicoPrimaryIssue, error) {
	return nil, nil
}
